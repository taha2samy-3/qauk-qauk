//go:build contract && load

package contracttest

// Load test (same fixture/protocol as the contract suite, so it runs against
// Django and Go alike). Run with GO_TEST_FLAGS="-tags contract,load -run TestLoad".
//
//	LOAD_DEVICES   device sockets (all for device "main")   default 20
//	LOAD_RATE      messages/s per device socket              default 10
//	LOAD_BROWSERS  browser sockets subscribed to "sensor"     default 100
//	LOAD_DURATION  send duration                              default 20s
//	LOAD_IDLE      extra idle browser sockets to open         default 1000
//	LOAD_OUT       optional JSON results file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func envInt(k string, d int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return d
}

type loadResult struct {
	Devices, RatePerDevice, Browsers int
	DurationS                        float64
	Sent, Expected, Delivered        int64
	LossPct                          float64
	DeliveriesPerSec                 float64
	P50ms, P95ms, P99ms, Maxms       float64
	IdleRequested, IdleAccepted      int
	IdleConnectS                     float64
}

func TestLoad(t *testing.T) {
	nDev, rate, nBr := envInt("LOAD_DEVICES", 20), envInt("LOAD_RATE", 10), envInt("LOAD_BROWSERS", 100)
	dur := time.Duration(envInt("LOAD_DURATION", 20)) * time.Second
	nIdle := envInt("LOAD_IDLE", 1000)
	el := elem(t, "sensor")
	rc := user(t, "rc")
	main := dev(t, "main")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dial := func(path string, hdr map[string][]string) (*websocket.Conn, error) {
		dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
		defer dcancel()
		c, _, err := websocket.Dial(dctx, fx.WSBase+path, &websocket.DialOptions{HTTPHeader: hdr})
		if c != nil {
			c.SetReadLimit(1 << 20)
		}
		return c, err
	}

	// --- browsers ---
	var delivered atomic.Int64
	var latMu sync.Mutex
	lat := make([]float64, 0, 1<<20)
	var subscribed sync.WaitGroup
	for i := range nBr {
		c, err := dial(fx.Paths.Browser, browserHeader(rc.Cookie, fx.Origin))
		if err != nil {
			t.Fatalf("browser %d: %v", i, err)
		}
		defer func() { _ = c.CloseNow() }()
		subscribed.Add(1)
		go func() {
			b, _ := json.Marshal(map[string]any{"type": "subscribe", "element_id": el.ID})
			_ = c.Write(ctx, websocket.MessageText, b)
			confirmed := false
			local := make([]float64, 0, 4096)
			defer func() {
				latMu.Lock()
				lat = append(lat, local...)
				latMu.Unlock()
			}()
			for {
				_, data, err := c.Read(ctx)
				if err != nil {
					if !confirmed {
						subscribed.Done()
					}
					return
				}
				var f struct {
					Type    string `json:"type"`
					Message struct {
						TS int64 `json:"ts"`
					} `json:"message"`
				}
				if json.Unmarshal(data, &f) != nil {
					continue
				}
				if f.Type == "subscribe" && !confirmed {
					confirmed = true
					subscribed.Done()
					continue
				}
				if f.Type == "message_element" && f.Message.TS > 0 && confirmed {
					delivered.Add(1)
					local = append(local, float64(time.Now().UnixNano()-f.Message.TS)/1e6)
				}
			}
		}()
	}
	subscribed.Wait()
	time.Sleep(time.Second) // history replay frames drain (they carry ts=0 or old ts)
	delivered.Store(0)
	latMu.Lock()
	lat = lat[:0]
	latMu.Unlock()

	// --- devices ---
	var sent atomic.Int64
	var seq atomic.Int64
	var devWG sync.WaitGroup
	start := time.Now()
	for i := range nDev {
		c, err := dial(fx.Paths.Device, deviceHeader(deviceToken(t, main)))
		if err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
		defer func() { _ = c.CloseNow() }()
		go func() { // devices also receive each other's frames; keep reading
			for {
				if _, _, err := c.Read(ctx); err != nil {
					return
				}
			}
		}()
		devWG.Add(1)
		go func() {
			defer devWG.Done()
			tick := time.NewTicker(time.Second / time.Duration(rate))
			defer tick.Stop()
			stop := time.After(dur)
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					b, _ := json.Marshal(map[string]any{"element_id": el.ID, "message": map[string]any{
						"value": seq.Add(1), "ts": time.Now().UnixNano()}})
					if err := c.Write(ctx, websocket.MessageText, b); err != nil {
						return
					}
					sent.Add(1)
				}
			}
		}()
	}
	devWG.Wait()
	elapsed := time.Since(start)
	time.Sleep(5 * time.Second) // let in-flight deliveries land

	cancel() // stop readers so latency slices are flushed
	time.Sleep(500 * time.Millisecond)
	latMu.Lock()
	slices.Sort(lat)
	pct := func(p float64) float64 {
		if len(lat) == 0 {
			return 0
		}
		return lat[min(len(lat)-1, int(p*float64(len(lat))))]
	}
	res := loadResult{Devices: nDev, RatePerDevice: rate, Browsers: nBr, DurationS: elapsed.Seconds(),
		Sent: sent.Load(), Delivered: delivered.Load(),
		P50ms: pct(0.50), P95ms: pct(0.95), P99ms: pct(0.99)}
	if len(lat) > 0 {
		res.Maxms = lat[len(lat)-1]
	}
	latMu.Unlock()
	res.Expected = res.Sent * int64(nBr)
	if res.Expected > 0 {
		res.LossPct = 100 * (1 - float64(res.Delivered)/float64(res.Expected))
	}
	res.DeliveriesPerSec = float64(res.Delivered) / elapsed.Seconds()

	// --- idle connection capacity ---
	if nIdle > 0 {
		ictx, icancel := context.WithCancel(context.Background())
		defer icancel()
		var accepted atomic.Int64
		var wg sync.WaitGroup
		sem := make(chan struct{}, 50)
		istart := time.Now()
		var conns sync.Map
		for i := range nIdle {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				dctx, dcancel := context.WithTimeout(ictx, 20*time.Second)
				defer dcancel()
				c, _, err := websocket.Dial(dctx, fx.WSBase+fx.Paths.Browser, &websocket.DialOptions{HTTPHeader: browserHeader(rc.Cookie, fx.Origin)})
				if err == nil {
					accepted.Add(1)
					conns.Store(i, c)
				}
			}()
		}
		wg.Wait()
		res.IdleRequested, res.IdleAccepted, res.IdleConnectS = nIdle, int(accepted.Load()), time.Since(istart).Seconds()
		time.Sleep(2 * time.Second)
		conns.Range(func(_, v any) bool { _ = v.(*websocket.Conn).CloseNow(); return true })
	}

	b, _ := json.MarshalIndent(res, "", "  ")
	t.Logf("load result:\n%s", b)
	fmt.Printf("LOAD_RESULT %s\n", mustCompact(b))
	if out := os.Getenv("LOAD_OUT"); out != "" {
		_ = os.WriteFile(out, b, 0o644)
	}
}

func mustCompact(b []byte) string {
	var v any
	_ = json.Unmarshal(b, &v)
	c, _ := json.Marshal(v)
	return string(c)
}
