package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/taha2samy/quackquack/server/internal/events"
	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
	"github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1/devicev1connect"
)

// grpcClient serves the device service over h2c and returns a gRPC client.
func grpcClient(t *testing.T, g *Gateway) devicev1connect.DeviceServiceClient {
	t.Helper()
	g.cfg.StreamMaxAge = time.Hour
	path, h := g.GRPCHandler()
	mux := http.NewServeMux()
	mux.Handle(path, h)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	tr := &http.Transport{Protocols: new(http.Protocols)}
	tr.Protocols.SetUnencryptedHTTP2(true)
	return devicev1connect.NewDeviceServiceClient(&http.Client{Transport: tr}, srv.URL, connect.WithGRPC())
}

func authed[T any](msg *T, tok string) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set("Authorization", "Bearer "+tok)
	return r
}

func value(t *testing.T, v any) *structpb.Value {
	t.Helper()
	pv, err := structpb.NewValue(v)
	if err != nil {
		t.Fatal(err)
	}
	return pv
}

func TestGRPCPublishAndList(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Rate: rate(5)}, events.ElementConfig{Name: "hum"})
	g.rest.last[td.cfg.Device.ID] = restLease{seen: time.Now().Add(time.Hour)}
	c := grpcClient(t, g)
	tok := td.token(t)
	ctx := context.Background()

	ts := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	resp, err := c.Publish(ctx, authed(&devicev1.PublishRequest{Messages: []*devicev1.DeviceMessage{
		{Element: "temp", Message: value(t, map[string]any{"value": 21.5}), Id: "m1", Ts: timestamppb.New(ts)},
		{Element: "nope", Message: value(t, 1)},
		{Element: "hum"},
	}}, tok))
	if err != nil {
		t.Fatal(err)
	}
	rs := resp.Msg.GetResults()
	if rs[0].GetStatus() != "accepted" || rs[0].GetElementId() != td.elem["temp"].String() || rs[0].GetEventId() == "" ||
		rs[1].GetCode() != "unknown_element" || rs[2].GetCode() != "invalid_message" {
		t.Fatalf("results %v", rs)
	}
	msgs := pub.messages(t)
	if len(msgs) != 1 || string(msgs[0].Message) != `{"value":21.5}` || !msgs[0].ClientTS.Equal(ts) {
		t.Fatalf("published %+v", msgs)
	}

	list, err := c.ListElements(ctx, authed(&devicev1.ListElementsRequest{}, tok))
	if err != nil || len(list.Msg.GetElements()) != 2 || list.Msg.GetElements()[0].GetRate() != 5 || list.Msg.GetDeviceId() != td.cfg.Device.ID.String() {
		t.Fatalf("list: %v %v", list, err)
	}

	_, err = c.Publish(ctx, connect.NewRequest(&devicev1.PublishRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("no token: %v", err)
	}
	_, err = c.Publish(ctx, authed(&devicev1.PublishRequest{}, tok))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty batch: %v", err)
	}
}

func TestGRPCPublishRateLimited(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "e", Rate: rate(1), Burst: burst(1)})
	g.rest.last[td.cfg.Device.ID] = restLease{seen: time.Now().Add(time.Hour)}
	c := grpcClient(t, g)
	req := func() error {
		_, err := c.Publish(context.Background(), authed(&devicev1.PublishRequest{Messages: []*devicev1.DeviceMessage{
			{Element: "e", Message: value(t, 1)}}}, td.token(t)))
		return err
	}
	if err := req(); err != nil {
		t.Fatal(err)
	}
	err := req()
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeResourceExhausted || ce.Meta().Get("Retry-After") != "1" {
		t.Fatalf("want RESOURCE_EXHAUSTED with Retry-After, got %v", err)
	}
}

func TestGRPCWatchGetsUserCommands(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "led"})
	c := grpcClient(t, g)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := c.Watch(ctx, authed(&devicev1.WatchRequest{}, td.token(t)))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(g.hub.DeviceClients(td.cfg.Device.ID)) == 1 })
	// the device's own message (e.g. via Publish) is not watched back
	own := msg(td.elem["led"], td.cfg.Device.ID, events.SourceDevice, 0)
	own.Actor = events.Actor{ID: td.cfg.Device.ID.String(), Name: "dev"}
	g.publishElement(own, "grpc")
	g.publishElement(msg(td.elem["led"], td.cfg.Device.ID, events.SourceUser, 1), "b1")
	if !stream.Receive() {
		t.Fatalf("no message: %v", stream.Err())
	}
	m := stream.Msg().GetMessage()
	if m.GetElement() != "led" || m.GetMessage().GetStructValue().GetFields()["value"].GetNumberValue() != 1 ||
		m.GetActor().GetId() != "7" || m.GetActor().GetName() != "alice" || m.GetTime() == nil {
		t.Fatalf("watch message %v", m)
	}
	cancel()
	waitFor(t, func() bool { return len(g.hub.DeviceClients(td.cfg.Device.ID)) == 0 })
}

func TestGRPCSessionPublishesAndReceives(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "led"})
	c := grpcClient(t, g)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := c.Session(ctx)
	stream.RequestHeader().Set("Authorization", "Bearer "+td.token(t))
	if err := stream.Send(&devicev1.SessionRequest{Publish: &devicev1.DeviceMessage{Element: "led", Message: value(t, map[string]any{"on": true})}}); err != nil {
		t.Fatal(err)
	}
	res, err := stream.Receive()
	if err != nil || res.GetResult().GetStatus() != "accepted" {
		t.Fatalf("publish result %v %v", res, err)
	}
	if msgs := pub.messages(t); len(msgs) != 1 || msgs[0].Origin.ConnID == "" {
		t.Fatalf("published %+v", msgs)
	}
	// its own message is not echoed; a user's is
	g.publishElement(msg(td.elem["led"], td.cfg.Device.ID, events.SourceUser, 2), "b1")
	res, err = stream.Receive()
	if err != nil || res.GetMessage().GetElement() != "led" {
		t.Fatalf("command %v %v", res, err)
	}
	// a server-side close (key changed) ends the stream with UNAUTHENTICATED
	for _, d := range g.hub.DeviceClients(td.cfg.Device.ID) {
		d.kill(closeRevoked, "device key changed")
	}
	_, err = stream.Receive()
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestGRPCStreamMaxAge(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "led"})
	c := grpcClient(t, g)
	g.cfg.StreamMaxAge = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := c.Watch(ctx, authed(&devicev1.WatchRequest{}, td.token(t)))
	if err != nil {
		t.Fatal(err)
	}
	if stream.Receive() {
		t.Fatal("unexpected message")
	}
	if connect.CodeOf(stream.Err()) != connect.CodeUnavailable {
		t.Fatalf("want UNAVAILABLE after max age, got %v", stream.Err())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
