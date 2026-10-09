package devtools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
	"github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1/devicev1connect"
)

// The simulator speaks every device transport, so it doubles as a reference
// client for each: WebSocket (protocol v1), REST (/device/v1) and gRPC.

// Transports a simulated device can use; "mixed" spreads devices over all three.
const (
	TransportWS    = "websocket"
	TransportREST  = "rest"
	TransportGRPC  = "grpc"
	TransportMixed = "mixed"
)

var transports = []string{TransportWS, TransportREST, TransportGRPC}

// transportFor picks the transport of the i-th demo device.
func transportFor(mode string, i int) (string, error) {
	switch mode {
	case "", "ws", TransportWS:
		return TransportWS, nil
	case TransportREST, TransportGRPC:
		return mode, nil
	case TransportMixed:
		return transports[i%len(transports)], nil
	}
	return "", fmt.Errorf("unknown transport %q (websocket, rest, grpc, mixed)", mode)
}

// deviceLink is one device's connection, whatever the transport.
type deviceLink interface {
	send(ctx context.Context, elementID string, msg any) error
	// recv blocks for the next message for the device (a user command).
	recv(ctx context.Context) (incomingFrame, error)
	close()
}

func dialLink(ctx context.Context, transport, base, token string) (deviceLink, error) {
	httpBase := "http" + strings.TrimPrefix(strings.TrimPrefix(base, "ws"), "http")
	switch transport {
	case TransportREST:
		return &restLink{base: httpBase, token: token, http: &http.Client{Timeout: 45 * time.Second}}, nil
	case TransportGRPC:
		return dialGRPC(ctx, httpBase, token)
	default:
		return dialWS(ctx, "ws"+strings.TrimPrefix(httpBase, "http"), token)
	}
}

// --- WebSocket (protocol v1) ---

type wsLink struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func dialWS(ctx context.Context, wsBase, token string) (*wsLink, error) {
	ws, _, err := websocket.Dial(ctx, wsBase+"/device/node_red/", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		return nil, err
	}
	return &wsLink{ws: ws}, nil
}

func (l *wsLink) send(ctx context.Context, elementID string, msg any) error {
	frame, _ := json.Marshal(map[string]any{"element_id": elementID, "message": msg})
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ws.Write(ctx, websocket.MessageText, frame)
}

func (l *wsLink) recv(ctx context.Context) (incomingFrame, error) {
	var in incomingFrame
	_, data, err := l.ws.Read(ctx)
	if err != nil {
		return in, err
	}
	return in, json.Unmarshal(data, &in)
}

func (l *wsLink) close() { _ = l.ws.Close(websocket.StatusNormalClosure, "bye") }

// --- REST (/device/v1): POST messages, long-poll sync for commands ---

type restLink struct {
	base, token string
	http        *http.Client
	cursor      string
	synced      bool // the first sync (current values, not waited for) is done
	pending     []incomingFrame
}

func (l *restLink) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+l.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, b)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (l *restLink) send(ctx context.Context, elementID string, msg any) error {
	return l.do(ctx, http.MethodPost, "/device/v1/messages", map[string]any{"element": elementID, "message": msg}, nil)
}

func (l *restLink) recv(ctx context.Context) (incomingFrame, error) {
	for len(l.pending) == 0 {
		var out struct {
			Cursor   string          `json:"cursor"`
			Messages []incomingFrame `json:"messages"`
		}
		wait := "30s"
		if !l.synced {
			wait = "0" // first call: learn the cursor, skip commands sent before this device came up
		}
		if err := l.do(ctx, http.MethodGet, "/device/v1/sync?wait="+wait+"&cursor="+l.cursor, nil, &out); err != nil {
			return incomingFrame{}, err
		}
		l.cursor = out.Cursor
		if !l.synced {
			l.synced = true
			continue
		}
		l.pending = out.Messages
	}
	f := l.pending[0]
	l.pending = l.pending[1:]
	return f, nil
}

func (l *restLink) close() {}

// --- gRPC (Session: publish and receive on one stream) ---

type grpcLink struct {
	stream *connect.BidiStreamForClient[devicev1.SessionRequest, devicev1.SessionResponse]
	mu     sync.Mutex
	cancel context.CancelFunc
}

func dialGRPC(ctx context.Context, httpBase, token string) (*grpcLink, error) {
	tr := &http.Transport{Protocols: new(http.Protocols)}
	tr.Protocols.SetUnencryptedHTTP2(true) // h2c; use TLS (https://) in production
	cl := devicev1connect.NewDeviceServiceClient(&http.Client{Transport: tr}, httpBase, connect.WithGRPC())
	sctx, cancel := context.WithCancel(ctx)
	stream := cl.Session(sctx)
	stream.RequestHeader().Set("Authorization", "Bearer "+token)
	if err := stream.Send(nil); err != nil { // open the stream now, to fail fast on auth
		cancel()
		return nil, err
	}
	return &grpcLink{stream: stream, cancel: cancel}, nil
}

func (l *grpcLink) send(_ context.Context, elementID string, msg any) error {
	b, _ := json.Marshal(msg)
	v := &structpb.Value{}
	if err := protojson.Unmarshal(b, v); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stream.Send(&devicev1.SessionRequest{Publish: &devicev1.DeviceMessage{Element: elementID, Message: v}})
}

func (l *grpcLink) recv(context.Context) (incomingFrame, error) {
	for {
		res, err := l.stream.Receive()
		if err != nil {
			return incomingFrame{}, err
		}
		if res.GetResult() != nil {
			continue // publish acknowledgements
		}
		m := res.GetMessage()
		raw, _ := protojson.Marshal(m.GetMessage())
		f := incomingFrame{ElementID: m.GetElementId(), Message: raw}
		f.Auth.UserID, _ = json.Marshal(m.GetActor().GetId())
		return f, nil
	}
}

func (l *grpcLink) close() {
	l.mu.Lock()
	_ = l.stream.CloseRequest()
	l.mu.Unlock()
	l.cancel()
}

// Token prints a device JWT for a demo device (by name or id), for trying
// the REST and gRPC APIs by hand (curl, buf curl).
func Token(file, device string, ttl time.Duration) (string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	var demo DemoFile
	if err := json.Unmarshal(b, &demo); err != nil {
		return "", err
	}
	for _, d := range demo.Devices {
		if d.Name != device && d.ID != device {
			continue
		}
		key, err := parsePrivate(d.PrivateKeyPEM)
		if err != nil {
			return "", err
		}
		return jwt.NewWithClaims(jwt.GetSigningMethod(d.Alg), jwt.MapClaims{"id": d.ID, "iat": time.Now().Unix(),
			"exp": time.Now().Add(ttl).Unix()}).SignedString(key)
	}
	return "", fmt.Errorf("no demo device %q in %s", device, file)
}
