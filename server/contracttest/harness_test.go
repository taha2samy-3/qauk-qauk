//go:build contract

// Package contracttest is a black-box WebSocket contract suite. It talks to a
// backend only through WebSockets, a fixture file ($CONTRACT_FIXTURE) and a
// hook command ($CONTRACT_HOOK). See README.md for the interface.
package contracttest

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
)

// Timing knobs. Generous on purpose: the suite must be stable, not fast.
const (
	recvTimeout   = 3 * time.Second        // max wait for an expected frame
	quietWindow   = 700 * time.Millisecond // wait for "must NOT receive" assertions
	rejectWindow  = 1500 * time.Millisecond
	settleDelay   = 300 * time.Millisecond // after closing a device socket / mutating state
	hookTimeout   = 30 * time.Second
	propagateWait = 500 * time.Millisecond // after hooks that change device-side state
	closeCode4000 = websocket.StatusCode(4000)
)

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

type Device struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	KeyID         string `json:"key_id"`
	PrivateKeyPEM string `json:"private_key_pem"`
	Alg           string `json:"alg"`
}

type Element struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	DeviceID string          `json:"device_id"`
	Points   int             `json:"points"`
	Details  json.RawMessage `json:"details"`
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Cookie   string `json:"cookie"`
}

type Fixture struct {
	WSBase string `json:"ws_base"`
	Origin string `json:"origin"`
	Paths  struct {
		Device  string `json:"device"`
		Browser string `json:"browser"`
	} `json:"paths"`
	Devices  map[string]Device  `json:"devices"`
	Elements map[string]Element `json:"elements"`
	Users    map[string]User    `json:"users"`
	Groups   map[string]string  `json:"groups"`
}

var (
	fx        *Fixture
	knownBugs = map[string]bool{}
)

func TestMain(m *testing.M) {
	path := os.Getenv("CONTRACT_FIXTURE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "CONTRACT_FIXTURE is not set; see contracttest/README.md")
		os.Exit(2)
	}
	if os.Getenv("CONTRACT_HOOK") == "" {
		fmt.Fprintln(os.Stderr, "CONTRACT_HOOK is not set; see contracttest/README.md")
		os.Exit(2)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read fixture:", err)
		os.Exit(2)
	}
	fx = &Fixture{}
	if err := json.Unmarshal(raw, fx); err != nil {
		fmt.Fprintln(os.Stderr, "parse fixture:", err)
		os.Exit(2)
	}
	if fx.Paths.Device == "" {
		fx.Paths.Device = "/device/node_red/"
	}
	if fx.Paths.Browser == "" {
		fx.Paths.Browser = "/browser/simple/"
	}
	for _, b := range strings.Split(os.Getenv("CONTRACT_KNOWN_BUGS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			knownBugs[strings.ToUpper(b)] = true
		}
	}
	os.Exit(m.Run())
}

func dev(t *testing.T, role string) Device {
	t.Helper()
	d, ok := fx.Devices[role]
	if !ok {
		t.Fatalf("fixture: no device %q", role)
	}
	return d
}

func elem(t *testing.T, role string) Element {
	t.Helper()
	e, ok := fx.Elements[role]
	if !ok {
		t.Fatalf("fixture: no element %q", role)
	}
	return e
}

func user(t *testing.T, role string) User {
	t.Helper()
	u, ok := fx.Users[role]
	if !ok {
		t.Fatalf("fixture: no user %q", role)
	}
	return u
}

// ---------------------------------------------------------------------------
// Known-bug mechanism
// ---------------------------------------------------------------------------

// checkBug reports the outcome of a test that asserts the FIXED behaviour of
// bug id. fixed=true means the fixed behaviour was observed.
//
//	listed & !fixed -> Skip ("known bug reproduced")
//	listed &  fixed -> Fail ("appears fixed; remove from CONTRACT_KNOWN_BUGS")
//	unlisted        -> normal pass/fail
func checkBug(t *testing.T, id string, fixed bool, format string, args ...any) {
	t.Helper()
	detail := fmt.Sprintf(format, args...)
	switch {
	case knownBugs[id] && !fixed:
		t.Skipf("known bug %s reproduced: %s", id, detail)
	case knownBugs[id] && fixed:
		t.Fatalf("bug %s appears fixed; remove from CONTRACT_KNOWN_BUGS", id)
	case !fixed:
		t.Fatalf("bug %s: %s", id, detail)
	}
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

// hook runs $CONTRACT_HOOK <action> <args...> and returns trimmed stdout.
func hook(t *testing.T, action string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CONTRACT_HOOK"), append([]string{action}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("hook %s %v failed: %v\nstdout: %s\nstderr: %s", action, args, err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// ---------------------------------------------------------------------------
// JWT
// ---------------------------------------------------------------------------

func parsePrivateKey(t *testing.T, pemStr string) crypto.Signer {
	t.Helper()
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		t.Fatalf("fixture: bad private key PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return k.(crypto.Signer)
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k
	}
	t.Fatalf("fixture: unsupported private key")
	return nil
}

func signingMethod(t *testing.T, alg string) jwt.SigningMethod {
	t.Helper()
	switch alg {
	case "RS256":
		return jwt.SigningMethodRS256
	case "ES256":
		return jwt.SigningMethodES256
	}
	t.Fatalf("unsupported alg %q", alg)
	return nil
}

// signJWT signs claims with the device's fixture key and algorithm.
func signJWT(t *testing.T, d Device, claims jwt.MapClaims) string {
	t.Helper()
	return signWith(t, signingMethod(t, d.Alg), parsePrivateKey(t, d.PrivateKeyPEM), claims)
}

func signWith(t *testing.T, m jwt.SigningMethod, key crypto.Signer, claims jwt.MapClaims) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(m, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return tok
}

// deviceToken is the normal token: {id: device UUID, exp: now+1h}.
func deviceToken(t *testing.T, d Device) string {
	return signJWT(t, d, jwt.MapClaims{"id": d.ID, "exp": time.Now().Add(time.Hour).Unix()})
}

func freshRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// ---------------------------------------------------------------------------
// WebSocket client
// ---------------------------------------------------------------------------

// Frame is one received text frame.
type Frame struct {
	Raw string
	M   map[string]any
}

func (f Frame) Str(k string) string {
	s, _ := f.M[k].(string)
	return s
}

func (f Frame) Type() string { return f.Str("type") }

// Client wraps a socket with a background reader so tests can wait with
// timeouts without the library closing the socket on context expiry.
type Client struct {
	t      *testing.T
	name   string
	c      *websocket.Conn
	frames chan Frame
	done   chan struct{}

	mu       sync.Mutex
	readErr  error
	gotFrame bool
}

type dialResult struct {
	client *Client
	resp   *http.Response
	err    error
}

func dial(t *testing.T, name, path string, hdr http.Header) dialResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), recvTimeout)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, fx.WSBase+path, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return dialResult{resp: resp, err: err}
	}
	c.SetReadLimit(4 << 20)
	cl := &Client{t: t, name: name, c: c, frames: make(chan Frame, 1024), done: make(chan struct{})}
	go cl.readLoop()
	t.Cleanup(func() { cl.Close() })
	return dialResult{client: cl, resp: resp}
}

func (cl *Client) readLoop() {
	defer close(cl.done)
	defer close(cl.frames)
	for {
		_, data, err := cl.c.Read(context.Background())
		if err != nil {
			cl.mu.Lock()
			cl.readErr = err
			cl.mu.Unlock()
			return
		}
		f := Frame{Raw: string(data)}
		_ = json.Unmarshal(data, &f.M)
		cl.mu.Lock()
		cl.gotFrame = true
		cl.mu.Unlock()
		cl.frames <- f
	}
}

// Close closes the socket (idempotent) and waits for the reader to stop.
func (cl *Client) Close() {
	_ = cl.c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-cl.done:
	case <-time.After(recvTimeout):
		_ = cl.c.CloseNow()
	}
}

// Closed reports whether the reader has stopped, and the close status (-1 if
// the socket ended without a close frame).
func (cl *Client) closeStatus() websocket.StatusCode {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return websocket.CloseStatus(cl.readErr)
}

func (cl *Client) Send(v any) {
	cl.t.Helper()
	var data []byte
	switch x := v.(type) {
	case string:
		data = []byte(x)
	case []byte:
		data = x
	default:
		var err error
		if data, err = json.Marshal(v); err != nil {
			cl.t.Fatalf("%s: marshal: %v", cl.name, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), recvTimeout)
	defer cancel()
	if err := cl.c.Write(ctx, websocket.MessageText, data); err != nil {
		cl.t.Fatalf("%s: write: %v", cl.name, err)
	}
}

// next returns the next frame within d. ok=false on timeout or socket end.
func (cl *Client) next(d time.Duration) (Frame, bool) {
	select {
	case f, ok := <-cl.frames:
		return f, ok
	case <-time.After(d):
		return Frame{}, false
	}
}

// Expect waits up to recvTimeout for a frame matching pred, discarding (and
// logging) non-matching frames. Fails the test on timeout.
func (cl *Client) Expect(desc string, pred func(Frame) bool) Frame {
	cl.t.Helper()
	f, ok := cl.TryExpect(recvTimeout, pred)
	if !ok {
		cl.t.Fatalf("%s: timed out after %s waiting for %s (close status %v)", cl.name, recvTimeout, desc, cl.closeStatus())
	}
	return f
}

// TryExpect is Expect without failing.
func (cl *Client) TryExpect(d time.Duration, pred func(Frame) bool) (Frame, bool) {
	cl.t.Helper()
	deadline := time.Now().Add(d)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return Frame{}, false
		}
		f, ok := cl.next(left)
		if !ok {
			return Frame{}, false
		}
		if pred(f) {
			return f, true
		}
		cl.t.Logf("%s: skipping frame %s", cl.name, truncate(f.Raw, 300))
	}
}

// ExpectNone asserts that no frame matching pred arrives within quietWindow.
func (cl *Client) ExpectNone(desc string, pred func(Frame) bool) {
	cl.t.Helper()
	if f, ok := cl.TryExpect(quietWindow, pred); ok {
		cl.t.Fatalf("%s: unexpectedly received %s: %s", cl.name, desc, truncate(f.Raw, 500))
	}
}

// Drain discards frames until the socket has been quiet for quietWindow.
func (cl *Client) Drain() {
	for {
		if _, ok := cl.next(quietWindow); !ok {
			return
		}
	}
}

// WaitClosed waits for the server to close the socket; returns the close
// status (-1 when there was no close frame) and whether it closed at all.
func (cl *Client) WaitClosed(d time.Duration) (websocket.StatusCode, bool) {
	deadline := time.After(d)
	for {
		select {
		case _, ok := <-cl.frames:
			if !ok {
				<-cl.done
				return cl.closeStatus(), true
			}
		case <-deadline:
			return 0, false
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// ---------------------------------------------------------------------------
// Device / browser connections
// ---------------------------------------------------------------------------

func deviceHeader(token string) http.Header {
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return h
}

func browserHeader(cookie, origin string) http.Header {
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", cookie)
	}
	if origin != "" {
		h.Set("Origin", origin)
	}
	return h
}

// connectDevice opens an authenticated device socket and fails if rejected.
// Closing it in cleanup is followed by settleDelay so the server's
// disconnect bookkeeping does not race the next test's connection.
func connectDevice(t *testing.T, role string) *Client {
	t.Helper()
	d := dev(t, role)
	return mustAccept(t, "device:"+role, fx.Paths.Device, deviceHeader(deviceToken(t, d)), true)
}

func connectBrowser(t *testing.T, role string) *Client {
	t.Helper()
	u := user(t, role)
	return mustAccept(t, "browser:"+role, fx.Paths.Browser, browserHeader(u.Cookie, fx.Origin), false)
}

func mustAccept(t *testing.T, name, path string, hdr http.Header, isDevice bool) *Client {
	t.Helper()
	if isDevice {
		settleAfterClose(t)
	}
	r := dial(t, name, path, hdr)
	if r.err != nil {
		t.Fatalf("%s: connect failed: %v (%s)", name, r.err, respStatus(r.resp))
	}
	return r.client
}

// settleAfterClose registers a cleanup that sleeps settleDelay. Register it
// BEFORE dialing: cleanups run LIFO, so it then runs after the socket close.
func settleAfterClose(t *testing.T) {
	t.Cleanup(func() { time.Sleep(settleDelay) })
}

func respStatus(resp *http.Response) string {
	if resp == nil {
		return "no HTTP response"
	}
	return "HTTP " + resp.Status
}

// connOutcome classifies a connection attempt.
type connOutcome struct {
	rejected bool
	how      string
	client   *Client
}

// attempt dials and classifies the result. Rejected = failed handshake (e.g.
// HTTP 403) OR a close with code 4000 before any data frame. Otherwise the
// socket is considered accepted after rejectWindow of silence.
func attempt(t *testing.T, name, path string, hdr http.Header) connOutcome {
	t.Helper()
	r := dial(t, name, path, hdr)
	if r.err != nil {
		if r.resp != nil {
			return connOutcome{rejected: true, how: "handshake rejected: " + respStatus(r.resp)}
		}
		return connOutcome{rejected: true, how: "handshake failed: " + r.err.Error()}
	}
	cl := r.client
	select {
	case f, ok := <-cl.frames:
		if ok {
			// A data frame arrived: accepted. Put nothing back; callers of
			// attempt don't need it.
			return connOutcome{how: "accepted (data frame: " + truncate(f.Raw, 120) + ")", client: cl}
		}
		<-cl.done
		st := cl.closeStatus()
		if st == closeCode4000 {
			return connOutcome{rejected: true, how: "closed with 4000 after handshake", client: cl}
		}
		return connOutcome{how: fmt.Sprintf("accepted then closed with %v", st), client: cl}
	case <-time.After(rejectWindow):
		return connOutcome{how: "accepted (open after " + rejectWindow.String() + ")", client: cl}
	}
}

// ---------------------------------------------------------------------------
// Protocol helpers
// ---------------------------------------------------------------------------

var nonceMu sync.Mutex

// uniqueValue returns a number that is unique for the run, so tests can match
// frames regardless of history replay or other traffic.
func uniqueValue() int64 {
	nonceMu.Lock()
	defer nonceMu.Unlock()
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<40))
	return n.Int64() + 1
}

// numEq compares a JSON-decoded value with an int64.
func numEq(v any, want int64) bool {
	f, ok := v.(float64)
	return ok && int64(f) == want
}

// idString normalises a JSON id (number or string) to a string.
func idString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func messageValue(f Frame) (any, bool) {
	m, ok := f.M["message"].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := m["value"]
	return v, ok
}

// isElementMsg matches a browser-side message_element frame for element with
// message.value == value.
func isElementMsg(elementID string, value int64) func(Frame) bool {
	return func(f Frame) bool {
		if f.Type() != "message_element" || f.Str("element_id") != elementID {
			return false
		}
		v, ok := messageValue(f)
		return ok && numEq(v, value)
	}
}

// isDeviceMsg matches a device-side frame (no type field) for element/value.
func isDeviceMsg(elementID string, value int64) func(Frame) bool {
	return func(f Frame) bool {
		if f.Str("element_id") != elementID {
			return false
		}
		v, ok := messageValue(f)
		return ok && numEq(v, value)
	}
}

func isType(typ, elementID string) func(Frame) bool {
	return func(f Frame) bool {
		return f.Type() == typ && (elementID == "" || f.Str("element_id") == elementID)
	}
}

func isAnyElementMsg(elementID string) func(Frame) bool {
	return isType("message_element", elementID)
}

// subscribe sends a subscribe request and returns the confirmation frame.
func subscribe(t *testing.T, b *Client, elementID string) Frame {
	t.Helper()
	b.Send(map[string]any{"type": "subscribe", "element_id": elementID})
	f := b.Expect("subscribe confirm for "+elementID, func(f Frame) bool {
		return (f.Type() == "subscribe" || f.Type() == "error") && f.Str("element_id") == elementID
	})
	if f.Type() != "subscribe" {
		t.Fatalf("%s: subscribe %s failed: %s", b.name, elementID, f.Raw)
	}
	return f
}

// subscribeAndDrain subscribes and drains history replay so later
// assertions only see live traffic.
func subscribeAndDrain(t *testing.T, b *Client, elementID string) Frame {
	t.Helper()
	f := subscribe(t, b, elementID)
	b.Drain()
	return f
}

func deviceSend(d *Client, elementID string, value int64) {
	d.Send(map[string]any{"element_id": elementID, "message": map[string]any{"value": value}})
}

func browserSend(b *Client, elementID string, value int64) {
	b.Send(map[string]any{"type": "message_element", "element_id": elementID, "message": map[string]any{"value": value}})
}
