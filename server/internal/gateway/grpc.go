package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/taha2samy/quackquack/server/internal/authn"
	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
	"github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1/devicev1connect"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/registry"
)

// gRPC device adapter (connect-go: serves gRPC, gRPC-Web and Connect). Same
// JWT and rules as the WebSocket and REST; see
// docs/04_api_reference/device_grpc_api.md.

// GRPCHandler returns the mount path and handler of the device service.
func (g *Gateway) GRPCHandler() (string, http.Handler) {
	return devicev1connect.NewDeviceServiceHandler(&grpcDevice{g: g}, connect.WithReadMaxBytes(restMaxBody))
}

type grpcDevice struct{ g *Gateway }

var _ devicev1connect.DeviceServiceHandler = (*grpcDevice)(nil)

func (g *Gateway) grpcAuth(ctx context.Context, h http.Header, peer string) (*registry.Device, *authn.DeviceKey, error) {
	token, ok := authn.BearerToken(h.Get("Authorization"))
	if !ok {
		metrics.WSRejected.WithLabelValues("grpc", "no_token").Inc()
		return nil, nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing bearer token"))
	}
	dev, dk, err := g.authenticateDevice(ctx, token)
	if err != nil {
		metrics.WSRejected.WithLabelValues("grpc", "auth").Inc()
		g.log.Info("gateway: grpc device rejected", "peer", peer, "err", err)
		return nil, nil, connect.NewError(connect.CodeUnauthenticated, errors.New("device authentication failed"))
	}
	if v := h.Get(DeviceHeader); v != "" && v != dev.ID.String() {
		metrics.WSRejected.WithLabelValues("grpc", "device_header").Inc()
		return nil, nil, connect.NewError(connect.CodePermissionDenied, errors.New(DeviceHeader+" must be the id in the token"))
	}
	return dev, dk, nil
}

func (s *grpcDevice) Publish(ctx context.Context, req *connect.Request[devicev1.PublishRequest]) (*connect.Response[devicev1.PublishResponse], error) {
	g := s.g
	dev, _, err := g.grpcAuth(ctx, req.Header(), req.Peer().Addr)
	if err != nil {
		return nil, err
	}
	g.rest.touch(g, dev.ID, "grpc", req.Peer().Addr, req.Header().Get("User-Agent"))
	metrics.MessagesIn.WithLabelValues("grpc").Inc()
	msgs := req.Msg.GetMessages()
	if len(msgs) == 0 || len(msgs) > restMaxItems {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("send 1 to %d messages", restMaxItems))
	}
	if err := g.allowDevice(dev.ID, len(msgs)); err != nil {
		return nil, rateLimitedError(err)
	}
	items := make([]restItem, len(msgs))
	for i, m := range msgs {
		items[i] = fromProto(m)
	}
	results, limited, wait := g.publishItems(dev, "grpc", items)
	if limited == len(items) {
		return nil, rateLimitedError(&RateLimitedError{Scope: "element", RetryAfter: wait})
	}
	out := &devicev1.PublishResponse{Results: make([]*devicev1.PublishResult, len(results))}
	for i, r := range results {
		out.Results[i] = toProtoResult(r)
	}
	return connect.NewResponse(out), nil
}

func (s *grpcDevice) ListElements(ctx context.Context, req *connect.Request[devicev1.ListElementsRequest]) (*connect.Response[devicev1.ListElementsResponse], error) {
	dev, _, err := s.g.grpcAuth(ctx, req.Header(), req.Peer().Addr)
	if err != nil {
		return nil, err
	}
	out := &devicev1.ListElementsResponse{DeviceId: dev.ID.String(), DeviceName: dev.Name}
	for _, e := range dev.Elements {
		out.Elements = append(out.Elements, &devicev1.Element{Id: e.ID.String(), Name: e.Name, Points: int32(min(e.Points, 1<<30)),
			Rate: e.Rate, Burst: int32(min(e.Burst, 1<<30)), OverLimit: e.OverLimit})
	}
	return connect.NewResponse(out), nil
}

func (s *grpcDevice) Watch(ctx context.Context, req *connect.Request[devicev1.WatchRequest], stream *connect.ServerStream[devicev1.WatchResponse]) error {
	g := s.g
	dev, dk, err := g.grpcAuth(ctx, req.Header(), req.Peer().Addr)
	if err != nil {
		return err
	}
	info := map[string]any{"client": req.Peer().Addr, "transport": "grpc-watch", "user_agent": req.Header().Get("User-Agent")}
	return g.runStream(ctx, dev, dk, info, func(frame []byte) error {
		m, err := g.frameToProto(dev.ID, frame)
		if err != nil || m.GetActor().GetId() == dev.ID.String() {
			return nil // unrenderable, or the device's own message (sent over another connection)
		}
		return stream.Send(&devicev1.WatchResponse{Message: m})
	}, func() error { return stream.Send(nil) }, nil)
}

// resultMarker prefixes queued publish results on a Session stream, to tell
// them from element frames (which are JSON objects).
const resultMarker = 0x01

func (s *grpcDevice) Session(ctx context.Context, stream *connect.BidiStream[devicev1.SessionRequest, devicev1.SessionResponse]) error {
	g := s.g
	dev, dk, err := g.grpcAuth(ctx, stream.RequestHeader(), stream.Peer().Addr)
	if err != nil {
		return err
	}
	info := map[string]any{"client": stream.Peer().Addr, "transport": "grpc-session", "user_agent": stream.RequestHeader().Get("User-Agent")}
	write := func(frame []byte) error {
		if len(frame) > 0 && frame[0] == resultMarker {
			var r restResult
			if json.Unmarshal(frame[1:], &r) != nil {
				return nil
			}
			return stream.Send(&devicev1.SessionResponse{Kind: &devicev1.SessionResponse_Result{Result: toProtoResult(r)}})
		}
		m, err := g.frameToProto(dev.ID, frame)
		if err != nil {
			return nil
		}
		return stream.Send(&devicev1.SessionResponse{Kind: &devicev1.SessionResponse_Message{Message: m}})
	}
	recv := func(d *deviceClient) error {
		for {
			req, err := stream.Receive()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			metrics.MessagesIn.WithLabelValues("grpc").Inc()
			res := restResult{Status: "rejected"}
			if err := g.allowDevice(d.deviceID, 1); err != nil {
				res.Code, res.Error = "rate_limited", err.Error()
			} else if cur, ok := g.registry.Lookup(d.deviceID); ok {
				var rs []restResult
				rs, _, _ = g.publishItems(cur, d.id, []restItem{fromProto(req.GetPublish())})
				res = rs[0]
			}
			b, _ := json.Marshal(res)
			d.Send(append([]byte{resultMarker}, b...))
		}
	}
	return g.runStream(ctx, dev, dk, info, write, func() error { return stream.Send(nil) }, recv)
}

// runStream serves one long-lived device stream: it joins the hub like a
// socket (receives messages for the device's elements), holds a presence
// lease, and ends when the client leaves, the server closes it (key change,
// shutdown) or it reaches its maximum age. open sends the response headers
// once the stream is registered: clients wait for them before reading.
func (g *Gateway) runStream(ctx context.Context, dev *registry.Device, dk *authn.DeviceKey, info map[string]any,
	write func([]byte) error, open func() error, recv func(*deviceClient) error) error {
	d := &deviceClient{
		client: newStreamClient(g.ctx, g.nextConnID(), "device", func(_ context.Context, frame []byte) error {
			return write(frame)
		}),
		deviceID: dev.ID, keyID: dk.KeyID, name: dev.Name, allowed: map[uuid.UUID]struct{}{},
	}
	g.hub.AddDevice(d, deviceElementInfos(dev))
	metrics.WSConnections.WithLabelValues("grpc").Inc()
	info["conn_id"] = d.id
	audit := g.deviceConnected(dev.ID, d.id, info)
	defer func() {
		d.cancel()
		g.hub.RemoveDevice(d)
		metrics.WSConnections.WithLabelValues("grpc").Dec()
		g.deviceDisconnected(dev.ID, d.id, audit)
	}()

	if err := open(); err != nil { // before writeLoop: one writer at a time
		return err
	}
	written := make(chan struct{})
	go func() { d.writeLoop(); close(written) }()
	recvErr := make(chan error, 1)
	if recv != nil {
		go func() { recvErr <- recv(d); d.cancel() }()
	}
	var maxAgeC <-chan time.Time // nil: no maximum age
	if age := g.cfg.StreamMaxAge; age > 0 {
		maxAge := time.NewTimer(age + time.Duration(rand.Int64N(int64(age/10)+1)))
		defer maxAge.Stop()
		maxAgeC = maxAge.C
	}

	var err error
	select {
	case <-ctx.Done(): // the client left
	case <-d.ctx.Done(): // killed by the server, write failed, or the client closed its side
	case err = <-recvErr:
	case <-maxAgeC:
		d.kill(websocket.StatusGoingAway, "stream max age reached, reconnect")
	}
	d.cancel()
	<-written
	if err != nil && ctx.Err() == nil {
		return err
	}
	if code, reason := d.closeStatus(); reason != "" {
		if code == closeRevoked {
			return connect.NewError(connect.CodeUnauthenticated, errors.New(reason))
		}
		return connect.NewError(connect.CodeUnavailable, errors.New(reason))
	}
	return nil
}

func rateLimitedError(err error) error {
	ce := connect.NewError(connect.CodeResourceExhausted, err)
	var rl *RateLimitedError
	if errors.As(err, &rl) {
		ce.Meta().Set("Retry-After", fmt.Sprint(max(1, int(rl.RetryAfter.Seconds()+0.999))))
	}
	return ce
}

func fromProto(m *devicev1.DeviceMessage) restItem {
	it := restItem{Element: m.GetElement(), ID: m.GetId()}
	if v := m.GetMessage(); v != nil {
		if b, err := protojson.Marshal(v); err == nil {
			it.Message = b
		}
	}
	if ts := m.GetTs(); ts != nil && ts.IsValid() {
		t := ts.AsTime()
		it.TS = &t
	}
	return it
}

func toProtoResult(r restResult) *devicev1.PublishResult {
	return &devicev1.PublishResult{ElementId: r.ElementID, Status: r.Status, EventId: r.EventID, Code: r.Code, Error: r.Error}
}

// frameToProto converts a WebSocket device frame into an ElementMessage.
func (g *Gateway) frameToProto(deviceID uuid.UUID, frame []byte) (*devicev1.ElementMessage, error) {
	var f struct {
		ElementID  string          `json:"element_id"`
		Message    json.RawMessage `json:"message"`
		Auth       authJSON        `json:"auth"`
		LastEditAt string          `json:"last_edit_at"`
	}
	if err := json.Unmarshal(frame, &f); err != nil {
		return nil, err
	}
	v := &structpb.Value{}
	if err := protojson.Unmarshal(f.Message, v); err != nil {
		return nil, err
	}
	m := &devicev1.ElementMessage{ElementId: f.ElementID, Message: v,
		Actor: &devicev1.Actor{Id: unquote(f.Auth.UserID), Name: f.Auth.Username}}
	if t, err := time.Parse(time.RFC3339Nano, f.LastEditAt); err == nil {
		m.Time = timestamppb.New(t)
	}
	if dev, ok := g.registry.Lookup(deviceID); ok {
		if id, err := uuid.Parse(f.ElementID); err == nil {
			if el, ok := dev.Element(id); ok {
				m.Element = el.Name
			}
		}
	}
	return m, nil
}

// unquote renders auth.user_id (a JSON number or string) as a string.
func unquote(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
