//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/alert"
	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/secrets"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
	"github.com/taha2samy/quackquack/server/internal/webhook"
)

// receiver is a webhook endpoint that records signed requests.
type receiver struct {
	mu  sync.Mutex
	got []webhook.NotificationEvent
	hdr []http.Header
	raw [][]byte
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	var ce struct {
		Data webhook.NotificationEvent `json:"data"`
	}
	_ = json.Unmarshal(body, &ce)
	r.mu.Lock()
	r.got, r.hdr, r.raw = append(r.got, ce.Data), append(r.hdr, req.Header.Clone()), append(r.raw, body)
	r.mu.Unlock()
}

func (r *receiver) wait(t *testing.T, n int) []webhook.NotificationEvent {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.got) >= n {
			out := append([]webhook.NotificationEvent(nil), r.got...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("want %d notifications, got %d", n, len(r.got))
	return nil
}

// A rule fires once when the value crosses it, resolves once (after the
// hysteresis), survives an engine restart without repeating, and every
// notification is a signed, verified request.
func TestAlertsEndToEnd(t *testing.T) {
	ctx := context.Background()
	inst := startInstance(t, "gw-alerts", false)
	fix := setupRealtime(t, "alerts", 10)
	rcv := &receiver{}
	hook := httptest.NewServer(rcv)
	defer hook.Close()

	s := svc()
	s.WebhookAllowPrivate = true // the receiver is on 127.0.0.1
	ep, err := s.CreateWebhook(ctx, service.SystemActor, service.WebhookInput{Name: ptrTo("it"), URL: ptrTo(hook.URL),
		Severities: []string{"warning"}})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := s.WebhookEndpoint(ctx, ep.ID)
	secret, _ := s.Secrets.Open(stored.Secret)
	if _, err := s.SaveAlertRule(ctx, service.SystemActor, fix.elem.ID, service.AlertRuleInput{Name: "Hot", Condition: "above",
		Threshold: 30, Hysteresis: 2, Severity: "warning"}); err != nil {
		t.Fatal(err)
	}

	box, _ := secrets.NewBox("")
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	go (&webhook.Worker{DB: pool, Dispatcher: webhook.NewDispatcher(box, true), Workers: 2, Poll: 100 * time.Millisecond, Log: logger}).Run(workerCtx)
	group := bus.TopicName("quack-alerts-it-" + uuid.NewString()[:8])
	// the group starts where the test does: a slow join can't skip the first readings
	skipBacklog(t, group)
	startEngine := func() context.CancelFunc {
		ectx, cancel := context.WithCancel(ctx)
		go func() { _ = (&alert.Engine{Pool: pool, Brokers: brokers, Group: group, Log: logger}).Run(ectx) }()
		return cancel
	}
	stopEngine := startEngine()

	c := newClient(t, inst.URL)
	auth := deviceHeader(t, fix.key, fix.device.ID.String()).Get("Authorization")
	send := func(v float64) {
		t.Helper()
		r := c.do(http.MethodPost, "/device/v1/messages", []any{map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": v}}},
			"Authorization", auth)
		if r.Status != http.StatusOK {
			t.Fatalf("publish %v: %d %s", v, r.Status, r.Body)
		}
		time.Sleep(150 * time.Millisecond)
	}
	send(25)
	send(35) // fires
	send(36) // still firing: nothing
	got := rcv.wait(t, 1)
	if got[0].State != webhook.StateFiring || got[0].Value != 35.0 || got[0].ElementName != fix.elem.Name {
		t.Fatalf("firing notification: %+v", got[0])
	}

	// restart: the state is in the database, so no second "firing"
	stopEngine()
	stopEngine = startEngine()
	defer stopEngine()
	send(37)
	send(29) // within the hysteresis (resolves at ≤ 28)
	send(27) // resolves
	got = rcv.wait(t, 2)
	time.Sleep(time.Second)
	rcv.mu.Lock()
	n := len(rcv.got)
	rcv.mu.Unlock()
	if n != 2 || got[1].State != webhook.StateResolved || got[1].Severity != webhook.SeverityWarning {
		t.Fatalf("want exactly firing then resolved (warning), got %d: %+v", n, got)
	}
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	for i, h := range rcv.hdr {
		if !webhook.VerifyStandardWebhook(secret, h.Get("webhook-id"), h.Get("webhook-timestamp"), h.Get("webhook-signature"),
			rcv.raw[i], time.Now(), webhook.SignatureTolerance) {
			t.Fatalf("request %d isn't correctly signed", i)
		}
	}
	// the deliveries are recorded as delivered
	dl, err := s.ListWebhookDeliveries(ctx, ep.ID, 10)
	if err != nil || len(dl) != 2 || dl[0].Status != "delivered" || dl[1].Status != "delivered" {
		t.Fatalf("deliveries: %v %+v", err, dl)
	}
}

func ptrTo[T any](v T) *T { return &v }

// A rule on an attribute other than value fires on that attribute only (a
// numeric string counts, as in history), ignores messages without it, and the
// notification says which attribute it read. Changing the attribute starts
// the rule over. The attributes endpoint lists what the messages carry.
func TestAlertOnAttribute(t *testing.T) {
	ctx := context.Background()
	inst := startInstance(t, "gw-alert-attr", true) // with the ingester: the attributes come from history
	fix := setupRealtime(t, "alertattr", 10)
	rcv := &receiver{}
	hook := httptest.NewServer(rcv)
	defer hook.Close()

	s := svc()
	s.WebhookAllowPrivate = true
	if _, err := s.CreateWebhook(ctx, service.SystemActor, service.WebhookInput{Name: ptrTo("it"), URL: ptrTo(hook.URL)}); err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveAlertRule(ctx, service.SystemActor, fix.elem.ID, service.AlertRuleInput{Name: "Humid", Field: "climate.humidity",
		Condition: "above", Threshold: 80, Severity: "warning"})
	if err != nil || rule.Field != "climate.humidity" {
		t.Fatalf("save: %v %+v", err, rule)
	}

	box, _ := secrets.NewBox("")
	wctx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	go (&webhook.Worker{DB: pool, Dispatcher: webhook.NewDispatcher(box, true), Workers: 1, Poll: 100 * time.Millisecond, Log: logger}).Run(wctx)
	group := bus.TopicName("quack-alerts-it-" + uuid.NewString()[:8])
	skipBacklog(t, group)
	ectx, stopEngine := context.WithCancel(ctx)
	defer stopEngine()
	go func() { _ = (&alert.Engine{Pool: pool, Brokers: brokers, Group: group, Log: logger}).Run(ectx) }()

	c := newClient(t, inst.URL)
	auth := deviceHeader(t, fix.key, fix.device.ID.String()).Get("Authorization")
	send := func(msg map[string]any) {
		t.Helper()
		r := c.do(http.MethodPost, "/device/v1/messages", []any{map[string]any{"element": fix.elem.Name, "message": msg}}, "Authorization", auth)
		if r.Status != http.StatusOK {
			t.Fatalf("publish: %d %s", r.Status, r.Body)
		}
		time.Sleep(150 * time.Millisecond)
	}
	send(map[string]any{"value": 99})                                             // value is over 80, but not the attribute
	send(map[string]any{"value": 1, "climate": map[string]any{"humidity": "85"}}) // fires
	send(map[string]any{"value": 2})                                              // no attribute: no change
	send(map[string]any{"climate": map[string]any{"humidity": 70}})               // resolves
	got := rcv.wait(t, 2)
	time.Sleep(time.Second)
	rcv.mu.Lock()
	n := len(rcv.got)
	rcv.mu.Unlock()
	if n != 2 || got[0].State != webhook.StateFiring || got[0].Field != "climate.humidity" || got[0].Value != 85.0 ||
		got[1].State != webhook.StateResolved || got[1].Value != 70.0 {
		t.Fatalf("want firing at 85 then resolved at 70 on climate.humidity, got %d: %+v", n, got)
	}

	// the attributes the element's messages carry, newest values
	mkUser(t, "attr-admin", true)
	ac := newClient(t, inst.URL)
	ac.login("attr-admin", "attr-admin-password")
	have := map[string]float64{}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		r := ac.do(http.MethodGet, "/api/v1/admin/elements/"+fix.elem.ID.String()+"/attributes", nil)
		var attrs []struct {
			Field string  `json:"field"`
			Value float64 `json:"value"`
		}
		if r.Status != http.StatusOK || json.Unmarshal(r.Body, &attrs) != nil {
			t.Fatalf("attributes: %d %s", r.Status, r.Body)
		}
		have = map[string]float64{}
		for _, a := range attrs {
			have[a.Field] = a.Value
		}
		if _, ok := have["climate.humidity"]; ok { // the ingester wrote the last message
			break
		}
	}
	if have["climate.humidity"] != 70 || have["value"] != 2 {
		t.Fatalf("attributes: %+v", have)
	}

	// firing again, then moved to another attribute: the rule starts over
	send(map[string]any{"climate": map[string]any{"humidity": 90}})
	rcv.wait(t, 3)
	if _, err := s.SaveAlertRule(ctx, service.SystemActor, fix.elem.ID, service.AlertRuleInput{ID: &rule.ID, Name: "Humid",
		Field: "climate.dew", Condition: "above", Threshold: 80, Severity: "warning"}); err != nil {
		t.Fatal(err)
	}
	if firing, _ := store.AlertFiring(ctx, pool, rule.ID); firing {
		t.Fatal("a rule moved to another attribute must start over")
	}
}
