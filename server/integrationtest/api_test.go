//go:build integration

package integrationtest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/store"
)

func TestAuthFlow(t *testing.T) {
	in := startInstance(t, "gw-auth", false)
	mkUser(t, "auth-user", false)
	c := newClient(t, in.URL)

	r := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "auth-user", "password": "wrong-password"})
	if r.Status != 401 || !strings.Contains(r.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("bad login: %d %s %s", r.Status, r.Header.Get("Content-Type"), r.Body)
	}
	if r := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "nobody", "password": "whatever-pass"}); r.Status != 401 {
		t.Fatalf("unknown user: %d", r.Status)
	}
	r = c.must(200, "POST", "/api/v1/auth/login", map[string]string{"username": "auth-user", "password": "auth-user-password"}, nil)
	sc := r.Header.Get("Set-Cookie")
	for _, attr := range []string{"quack_session=", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(sc, attr) {
			t.Errorf("Set-Cookie %q lacks %s", sc, attr)
		}
	}
	var me struct {
		Username string `json:"username"`
		Password any    `json:"password_hash"`
	}
	c.must(200, "GET", "/api/v1/auth/me", nil, &me)
	if me.Username != "auth-user" || me.Password != nil {
		t.Fatalf("me: %+v", me)
	}
	c.must(204, "POST", "/api/v1/auth/logout", nil, nil)
	c.must(401, "GET", "/api/v1/auth/me", nil, nil)
}

func TestLoginRateLimit(t *testing.T) {
	in := startInstance(t, "gw-rl", false)
	mkUser(t, "rl-user", false)
	c := newClient(t, in.URL)
	last := 0
	for range 11 {
		last = c.do("POST", "/api/v1/auth/login", map[string]string{"username": "rl-user", "password": "nope-nope"}).Status
	}
	if last != 429 {
		t.Fatalf("11th failed attempt: %d, want 429", last)
	}
	// Even the right password is refused while limited.
	if s := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "rl-user", "password": "rl-user-password"}).Status; s != 429 {
		t.Fatalf("limited correct login: %d", s)
	}
}

func TestDjangoPasswordIsRehashedOnLogin(t *testing.T) {
	in := startInstance(t, "gw-rehash", false)
	ctx := context.Background()
	const djangoHash = "pbkdf2_sha256$1000000$POH1cDNkIXwzcmgW69izsU$4k5B6uBqBG2oHZH9oigY2q1LsiyxQeIR3D8AbcQLztc="
	if _, err := pool.Exec(ctx, `INSERT INTO users (username, password_hash) VALUES ('legacy', $1)`, djangoHash); err != nil {
		t.Fatal(err)
	}
	newClient(t, in.URL).login("legacy", "s3cret-Pass!")
	u, _ := store.GetUserByUsername(ctx, pool, "legacy")
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("hash not upgraded: %s", u.PasswordHash[:20])
	}
	newClient(t, in.URL).login("legacy", "s3cret-Pass!")
}

func TestAuthorization(t *testing.T) {
	in := startInstance(t, "gw-authz", false)
	mkUser(t, "plain", false)
	anon := newClient(t, in.URL)
	plain := newClient(t, in.URL)
	plain.login("plain", "plain-password")
	for _, p := range []string{"/api/v1/admin/users", "/api/v1/admin/devices", "/api/v1/admin/keys", "/api/v1/admin/permissions", "/api/v1/admin/audit"} {
		if s := anon.do("GET", p, nil).Status; s != 401 {
			t.Errorf("anon %s: %d", p, s)
		}
		if s := plain.do("GET", p, nil).Status; s != 403 {
			t.Errorf("non-admin %s: %d", p, s)
		}
	}
	if s := plain.do("POST", "/api/v1/admin/users", map[string]any{"username": "x", "password": "12345678"}).Status; s != 403 {
		t.Errorf("non-admin create user: %d", s)
	}
}

func TestCSRFProtection(t *testing.T) {
	in := startInstance(t, "gw-csrf", false)
	mkUser(t, "csrf-admin", true)
	c := newClient(t, in.URL)
	c.login("csrf-admin", "csrf-admin-password")
	body := map[string]any{"name": "evil-group"}
	if r := c.do("POST", "/api/v1/admin/groups", body, "Sec-Fetch-Site", "cross-site"); r.Status != 403 {
		t.Fatalf("cross-site (Sec-Fetch-Site) write: %d", r.Status)
	}
	if r := c.do("POST", "/api/v1/admin/groups", body, "Origin", "https://evil.example"); r.Status != 403 {
		t.Fatalf("cross-origin (Origin) write: %d", r.Status)
	}
	c.must(201, "POST", "/api/v1/admin/groups", map[string]any{"name": "good-group"}, nil)
	if r := c.do("GET", "/api/v1/admin/groups", nil, "Sec-Fetch-Site", "cross-site"); r.Status != 200 {
		t.Fatalf("cross-site GET must be allowed (safe method): %d", r.Status)
	}
}

func rsaPEM(t *testing.T, bits int) string {
	k, _ := rsa.GenerateKey(rand.Reader, bits)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestAdminCRUDAndValidation(t *testing.T) {
	in := startInstance(t, "gw-crud", false)
	mkUser(t, "crud-admin", true)
	mkUser(t, "crud-viewer", false)
	c := newClient(t, in.URL)
	c.login("crud-admin", "crud-admin-password")

	// keys
	if r := c.do("POST", "/api/v1/admin/keys", map[string]any{"name": "weak", "pem": rsaPEM(t, 1024)}); r.Status != 422 {
		t.Fatalf("weak RSA accepted: %d %s", r.Status, r.Body)
	}
	if r := c.do("POST", "/api/v1/admin/keys", map[string]any{"name": "junk", "pem": "hello"}); r.Status != 422 {
		t.Fatalf("junk PEM accepted: %d", r.Status)
	}
	var key store.Key
	c.must(201, "POST", "/api/v1/admin/keys", map[string]any{"name": "good", "pem": rsaPEM(t, 2048)}, &key)
	if key.Algorithm != "RS256" || key.KeySize != 2048 {
		t.Fatalf("key analysis: %+v", key)
	}

	// devices + elements
	var dev store.Device
	c.must(201, "POST", "/api/v1/admin/devices", map[string]any{"name": "crud-device", "public_key_id": key.ID}, &dev)
	if r := c.do("POST", "/api/v1/admin/elements", map[string]any{"device_id": dev.ID, "name": "e", "points": 1001}); r.Status != 422 {
		t.Fatalf("points 1001 accepted: %d", r.Status)
	}
	if r := c.do("POST", "/api/v1/admin/elements", map[string]any{"device_id": uuid.New(), "name": "e", "points": 1}); r.Status != 404 {
		t.Fatalf("element on unknown device: %d %s", r.Status, r.Body)
	}
	var el store.Element
	c.must(201, "POST", "/api/v1/admin/elements", map[string]any{"device_id": dev.ID, "name": "temp", "points": 10,
		"details": map[string]any{"unit": "C"}}, &el)
	c.must(200, "PATCH", "/api/v1/admin/devices/"+dev.ID.String(), map[string]any{"public_key_id": ""}, &dev)
	if dev.PublicKeyID != nil {
		t.Fatal("key not unassigned")
	}

	// permissions
	var users []store.User
	c.must(200, "GET", "/api/v1/admin/users", nil, &users)
	var viewerID int64
	for _, u := range users {
		if u.Username == "crud-viewer" {
			viewerID = u.ID
		}
	}
	if r := c.do("PUT", "/api/v1/admin/permissions", map[string]any{"element_id": el.ID, "user_id": viewerID, "group_id": 1, "permission": "R"}); r.Status != 422 {
		t.Fatalf("user+group accepted: %d", r.Status)
	}
	if r := c.do("PUT", "/api/v1/admin/permissions", map[string]any{"element_id": el.ID, "user_id": viewerID, "permission": "X"}); r.Status != 422 {
		t.Fatalf("bad permission accepted: %d", r.Status)
	}
	var p1, p2 store.Permission
	c.must(200, "PUT", "/api/v1/admin/permissions", map[string]any{"element_id": el.ID, "user_id": viewerID, "permission": "R"}, &p1)
	c.must(200, "PUT", "/api/v1/admin/permissions", map[string]any{"element_id": el.ID, "user_id": viewerID, "permission": "RC"}, &p2)
	if p1.ID != p2.ID || p2.Permission != "RC" {
		t.Fatalf("upsert not idempotent: %+v %+v", p1, p2)
	}
	v := newClient(t, in.URL)
	v.login("crud-viewer", "crud-viewer-password")
	var mine []store.UserElement
	v.must(200, "GET", "/api/v1/me/elements", nil, &mine)
	if len(mine) != 1 || mine[0].Permission != "RC" {
		t.Fatalf("me/elements: %+v", mine)
	}

	// duplicate username
	if r := c.do("POST", "/api/v1/admin/users", map[string]any{"username": "crud-viewer", "password": "12345678"}); r.Status != 409 {
		t.Fatalf("duplicate user: %d", r.Status)
	}
	// deactivation kills sessions
	c.must(200, "PATCH", fmt.Sprintf("/api/v1/admin/users/%d", viewerID), map[string]any{"is_active": false}, nil)
	if s := v.do("GET", "/api/v1/auth/me", nil).Status; s != 401 {
		t.Fatalf("deactivated user still authenticated: %d", s)
	}
	// cannot delete yourself
	var me struct{ ID int64 }
	c.must(200, "GET", "/api/v1/auth/me", nil, &me)
	if s := c.do("DELETE", fmt.Sprintf("/api/v1/admin/users/%d", me.ID), nil).Status; s != 422 {
		t.Fatalf("self-delete: %d", s)
	}
	// device delete cascades
	c.must(204, "DELETE", "/api/v1/admin/devices/"+dev.ID.String(), nil, nil)
	c.must(404, "GET", "/api/v1/admin/elements/"+el.ID.String(), nil, nil)

	var audit []store.AuditEntry
	c.must(200, "GET", "/api/v1/admin/audit?limit=100", nil, &audit)
	if len(audit) < 5 {
		t.Fatalf("audit log too short: %d", len(audit))
	}
}

func TestDashboardsOwnership(t *testing.T) {
	in := startInstance(t, "gw-dash", false)
	mkUser(t, "dash-a", false)
	mkUser(t, "dash-b", false)
	a, b := newClient(t, in.URL), newClient(t, in.URL)
	a.login("dash-a", "dash-a-password")
	b.login("dash-b", "dash-b-password")

	var d store.Dashboard
	a.must(201, "POST", "/api/v1/dashboards", map[string]any{"name": "Mine", "layout": map[string]any{"version": 1, "widgets": []any{}}}, &d)
	if r := a.do("POST", "/api/v1/dashboards", map[string]any{"name": "Bad", "layout": []int{1}}); r.Status != 422 {
		t.Fatalf("array layout accepted: %d", r.Status)
	}
	b.must(404, "GET", "/api/v1/dashboards/"+d.ID.String(), nil, nil)
	a.must(200, "PUT", "/api/v1/dashboards/"+d.ID.String(), map[string]any{"name": "Mine", "shared": true, "layout": map[string]any{"widgets": []any{map[string]any{"id": "w1"}}}}, &d)
	var got store.Dashboard
	b.must(200, "GET", "/api/v1/dashboards/"+d.ID.String(), nil, &got)
	if got.OwnerName != "dash-a" || !strings.Contains(string(got.Layout), "w1") {
		t.Fatalf("shared dashboard: %+v", got)
	}
	b.must(403, "PUT", "/api/v1/dashboards/"+d.ID.String(), map[string]any{"name": "hijack"}, nil)
	b.must(403, "DELETE", "/api/v1/dashboards/"+d.ID.String(), nil, nil)
	var list []store.Dashboard
	b.must(200, "GET", "/api/v1/dashboards", nil, &list)
	if len(list) != 1 {
		t.Fatalf("b sees %d dashboards", len(list))
	}
	a.must(204, "DELETE", "/api/v1/dashboards/"+d.ID.String(), nil, nil)
	b.must(404, "PUT", "/api/v1/dashboards/"+d.ID.String(), map[string]any{"name": "gone"}, nil)
}

func TestOutboxDeliversControlEvents(t *testing.T) {
	in := startInstance(t, "gw-outbox", false)
	mkUser(t, "ob-admin", true)
	c := newClient(t, in.URL)
	c.login("ob-admin", "ob-admin-password")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var g store.Group
	c.must(201, "POST", "/api/v1/admin/groups", map[string]any{"name": "ob-group"}, &g)
	var u struct{ ID int64 }
	c.must(201, "POST", "/api/v1/admin/users", map[string]any{"username": "ob-member", "password": "ob-member-pw"}, &u)

	// Read only what is published from now on: the topic outlives the DB, and
	// re-reading its whole history is slow under -race.
	admin, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatal(err)
	}
	ends, err := kadm.NewClient(admin).ListEndOffsets(ctx, bus.TopicName(events.TopicControlEvents))
	admin.Close()
	if err != nil || ends.Error() != nil {
		t.Fatalf("end offsets: %v %v", err, ends.Error())
	}
	from := map[string]map[int32]kgo.Offset{bus.TopicName(events.TopicControlEvents): {}}
	ends.Each(func(o kadm.ListedOffset) { from[o.Topic][o.Partition] = kgo.NewOffset().At(o.Offset) })
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumePartitions(from), kgo.FetchMaxWait(200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	start := time.Now()
	since := start.Add(-time.Second)
	c.must(204, "PUT", fmt.Sprintf("/api/v1/admin/groups/%d/members/%d", g.ID, u.ID), nil, nil)
	for ctx.Err() == nil {
		f := cl.PollFetches(ctx)
		var found bool
		f.EachRecord(func(r *kgo.Record) {
			var ev events.Event
			_ = json.Unmarshal(r.Value, &ev)
			var cc events.ControlChanged
			_ = ev.DecodeData(&cc)
			if cc.Kind == events.KindGroupMembership && cc.UserID != nil && *cc.UserID == u.ID && ev.Time.After(since) {
				found = true
				if string(r.Key) != ev.PartitionKey || hdr(r, "content-type") != events.ContentTypeHeader {
					t.Errorf("record key/header: key=%s header=%s", r.Key, hdr(r, "content-type"))
				}
			}
		})
		if found {
			t.Logf("membership event delivered in %s", time.Since(start))
			return
		}
	}
	t.Fatal("control event not delivered")
}

func hdr(r *kgo.Record, k string) string {
	for _, h := range r.Headers {
		if h.Key == k {
			return string(h.Value)
		}
	}
	return ""
}

func TestUnknownRoutesAndHealth(t *testing.T) {
	in := startInstance(t, "gw-health", false)
	c := newClient(t, in.URL)
	c.must(200, "GET", "/healthz", nil, nil)
	c.must(200, "GET", "/readyz", nil, nil)
	if r := c.do("GET", "/metrics", nil); r.Status != 200 || !strings.Contains(string(r.Body), "quack_ws_connections") {
		t.Fatalf("metrics: %d", r.Status)
	}
	if r := c.do("GET", "/api/openapi.json", nil); r.Status != 200 || !strings.Contains(string(r.Body), `"openapi":"3.1`) {
		t.Fatalf("openapi: %d", r.Status)
	}
	_ = http.StatusOK
}

func TestHistoryByField(t *testing.T) {
	in := startInstance(t, "gw-field", false)
	f := setupRealtime(t, "fieldh", 10)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	mk := func(i int, payload string) history.Event {
		return history.Event{Time: now.Add(time.Duration(i) * time.Second), ID: uuid.Must(uuid.NewV7()),
			ElementID: f.elem.ID, DeviceID: f.device.ID, Source: "device", ActorID: "d", ActorName: "d",
			Payload: json.RawMessage(payload)}
	}
	rows := []history.Event{
		mk(1, `{"climate":{"temp":20},"relay":"ON","level":"1.5","ok":true}`),
		mk(2, `{"climate":{"temp":24},"relay":"OFF","level":"2.5","ok":false}`),
		mk(3, `{"climate":{"temp":"n/a"},"sensors":[{"v":7}]}`),
	}
	if _, err := hist.Append(ctx, rows); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, in.URL)
	c.login("fieldh-user", "fieldh-user-password")
	q := func(field string) (avg float64, n int64) {
		var out struct {
			Buckets []struct {
				Avg *float64
				N   int64
			} `json:"buckets"`
		}
		c.must(200, "GET", "/api/v1/elements/"+f.elem.ID.String()+"/history?step=1h&field="+url.QueryEscape(field)+
			"&from="+url.QueryEscape(now.Add(-time.Minute).Format(time.RFC3339))+"&to="+url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339)), nil, &out)
		for _, b := range out.Buckets {
			n += b.N
			if b.Avg != nil {
				avg = *b.Avg
			}
		}
		return avg, n
	}
	if avg, n := q("climate.temp"); n != 2 || avg != 22 {
		t.Fatalf("climate.temp: avg=%v n=%d (non-numeric 'n/a' must be skipped)", avg, n)
	}
	if avg, n := q("level"); n != 2 || avg != 2 {
		t.Fatalf("numeric strings: avg=%v n=%d", avg, n)
	}
	if avg, n := q("ok"); n != 2 || avg != 0.5 {
		t.Fatalf("booleans: avg=%v n=%d", avg, n)
	}
	if avg, n := q("sensors[0].v"); n != 1 || avg != 7 {
		t.Fatalf("array path: avg=%v n=%d", avg, n)
	}
	if _, n := q("relay"); n != 0 {
		t.Fatalf("non-numeric strings must not aggregate: n=%d", n)
	}
	if r := c.do("GET", "/api/v1/elements/"+f.elem.ID.String()+"/history?step=1h&field="+url.QueryEscape("a;drop table x"), nil); r.Status != 422 {
		t.Fatalf("bad field path: %d", r.Status)
	}

	// cost guard: 1-minute buckets over two days is 2880 > QUACK_HISTORY_MAX_BUCKETS
	long := "/api/v1/elements/" + f.elem.ID.String() + "/history?step=1m&from=" +
		url.QueryEscape(now.Add(-48*time.Hour).Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.Format(time.RFC3339))
	if r := c.do("GET", long, nil); r.Status != 422 || !strings.Contains(string(r.Body), "limit is 1500") {
		t.Fatalf("too many buckets: %d %s", r.Status, r.Body)
	}

	// newest: the most recent events of a range, still in ascending order
	var raw struct {
		Events []struct {
			Message json.RawMessage `json:"message"`
		} `json:"events"`
	}
	c.must(200, "GET", "/api/v1/elements/"+f.elem.ID.String()+"/history?step=raw&limit=2&newest=true&from="+
		url.QueryEscape(now.Format(time.RFC3339))+"&to="+url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339)), nil, &raw)
	if len(raw.Events) != 2 || !strings.Contains(string(raw.Events[0].Message), `"temp":24`) || !strings.Contains(string(raw.Events[1].Message), `"sensors"`) {
		t.Fatalf("newest 2: %s", raw.Events)
	}
}
