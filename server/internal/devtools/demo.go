package devtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// DemoFile lists demo devices with their private keys so `simulate` can connect.
type DemoFile struct {
	Devices []DemoDevice `json:"devices"`
}

type DemoDevice struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Alg           string        `json:"alg"`
	PrivateKeyPEM string        `json:"private_key_pem"`
	Elements      []DemoElement `json:"elements"`
}

type DemoElement struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"` // sensor, chart, switch, slider
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Period float64 `json:"period_s"`
}

// Demo creates (additively) an admin, a viewer, a group and two demo devices
// with sensors and actuators, and writes their keys to out.
func Demo(ctx context.Context, pool *pgxpool.Pool, out, adminUser, adminPass string) error {
	svc := service.New(pool)
	devices, err := store.ListDevices(ctx, pool)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if d.Name == "Greenhouse A" || d.Name == "Boiler room" {
			fmt.Printf("demo already seeded (device %q exists); nothing to do. Keys: %s\n", d.Name, out)
			return nil
		}
	}
	a := service.Actor{Name: "demo"}
	admin, err := store.GetUserByUsername(ctx, pool, adminUser)
	if errors.Is(err, store.ErrNotFound) {
		admin, err = svc.CreateUser(ctx, a, service.UserInput{Username: adminUser, Password: adminPass, IsActive: true, IsAdmin: true})
	}
	if err != nil {
		return fmt.Errorf("admin user: %w", err)
	}
	viewer, err := store.GetUserByUsername(ctx, pool, "viewer")
	if errors.Is(err, store.ErrNotFound) {
		viewer, err = svc.CreateUser(ctx, a, service.UserInput{Username: "viewer", Password: "viewer12345", IsActive: true})
	}
	if err != nil {
		return err
	}
	grp, err := store.GetGroupByName(ctx, pool, "operators")
	if errors.Is(err, store.ErrNotFound) {
		grp, err = svc.CreateGroup(ctx, a, "operators")
	}
	if err != nil {
		return err
	}
	_ = svc.AddGroupMember(ctx, a, grp.ID, viewer.ID)

	type el struct {
		name, kind, details string
		min, max, period    float64
		points              int
	}
	plan := []struct {
		name, alg string
		elems     []el
	}{
		{"Greenhouse A", "ES256", []el{
			{"Temperature", "sensor", `{"title":"Temperature","unit":"°C","minValue":-10,"maxValue":50}`, 15, 32, 60, 100},
			{"Humidity", "sensor", `{"title":"Humidity","unit":"%","minValue":0,"maxValue":100}`, 40, 85, 90, 100},
			{"Soil moisture", "chart", `{"title":"Soil moisture","unit":"%"}`, 20, 60, 120, 300},
			{"Irrigation pump", "switch", `{"title":"Irrigation pump"}`, 0, 1, 0, 20},
			{"Fan speed", "slider", `{"title":"Fan speed","unit":"%","min":0,"max":100,"step":5}`, 0, 100, 0, 20},
		}},
		{"Boiler room", "RS256", []el{
			{"Water temperature", "sensor", `{"title":"Water temperature","unit":"°C","minValue":0,"maxValue":120}`, 55, 85, 45, 100},
			{"Pressure", "chart", `{"title":"Pressure","unit":"bar"}`, 1.2, 2.4, 30, 300},
			{"Burner", "switch", `{"title":"Burner"}`, 0, 1, 0, 20},
		}},
	}
	var file DemoFile
	for _, p := range plan {
		priv, pub, err := genKey(p.alg)
		if err != nil {
			return err
		}
		k, err := svc.CreateKey(ctx, a, "demo-"+p.name, pub, nil)
		if err != nil {
			return err
		}
		d, err := svc.CreateDevice(ctx, a, service.DeviceInput{Name: p.name, Description: "Demo device", PublicKeyID: &k.ID})
		if err != nil {
			return err
		}
		dd := DemoDevice{ID: d.ID.String(), Name: d.Name, Alg: p.alg, PrivateKeyPEM: priv}
		for _, e := range p.elems {
			created, err := svc.CreateElement(ctx, a, service.ElementInput{DeviceID: d.ID, Name: e.name, Points: e.points,
				Description: "Demo " + e.kind, Details: json.RawMessage(e.details)})
			if err != nil {
				return err
			}
			if _, err := svc.CreateStyle(ctx, a, store.Style{ElementID: created.ID, Name: "widget",
				Details: json.RawMessage(fmt.Sprintf(`{"widget":%q}`, e.kind))}); err != nil {
				return err
			}
			for _, sub := range []struct {
				user *int64
				grp  *int64
				perm string
			}{{&admin.ID, nil, "RC"}, {nil, &grp.ID, "R"}} {
				if _, err := svc.SetPermission(ctx, a, service.PermissionInput{ElementID: created.ID, UserID: sub.user, GroupID: sub.grp, Permission: sub.perm}); err != nil {
					return err
				}
			}
			dd.Elements = append(dd.Elements, DemoElement{ID: created.ID.String(), Name: e.name, Kind: e.kind, Min: e.min, Max: e.max, Period: e.period})
		}
		file.Devices = append(file.Devices, dd)
	}
	b, _ := json.MarshalIndent(file, "", "  ")
	if err := os.WriteFile(out, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("demo ready: admin=%s viewer=viewer/viewer12345 (group operators, read-only); keys in %s\n", adminUser, out)
	return nil
}

// Simulate connects every demo device and streams realistic values; it also
// echoes actuator commands back as state (like a real device would).
func Simulate(ctx context.Context, file, wsBase string, log *slog.Logger) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var demo DemoFile
	if err := json.Unmarshal(b, &demo); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, d := range demo.Devices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if err := simulateDevice(ctx, d, wsBase, log); err != nil && ctx.Err() == nil {
					log.Warn("simulator: device disconnected, retrying", "device", d.Name, "err", err)
					time.Sleep(3 * time.Second)
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

func simulateDevice(ctx context.Context, d DemoDevice, wsBase string, log *slog.Logger) error {
	key, err := parsePrivate(d.PrivateKeyPEM)
	if err != nil {
		return err
	}
	method := jwt.GetSigningMethod(d.Alg)
	tok, err := jwt.NewWithClaims(method, jwt.MapClaims{"id": d.ID, "iat": time.Now().Unix(), "exp": time.Now().Add(12 * time.Hour).Unix()}).SignedString(key)
	if err != nil {
		return err
	}
	ws, _, err := websocket.Dial(ctx, wsBase+"/device/node_red/", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}},
	})
	if err != nil {
		return err
	}
	defer func() { _ = ws.CloseNow() }()
	log.Info("simulator: connected", "device", d.Name)

	var mu sync.Mutex
	send := func(elementID string, msg any) error {
		frame, _ := json.Marshal(map[string]any{"element_id": elementID, "message": msg})
		mu.Lock()
		defer mu.Unlock()
		return ws.Write(ctx, websocket.MessageText, frame)
	}
	// Actuators: report initial state, then echo commands back as the new state.
	for _, e := range d.Elements {
		switch e.Kind {
		case "switch":
			_ = send(e.ID, map[string]any{"value": 0})
		case "slider":
			_ = send(e.ID, map[string]any{"value": 30})
		}
	}
	go func() {
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var in struct {
				ElementID string          `json:"element_id"`
				Message   json.RawMessage `json:"message"`
			}
			if json.Unmarshal(data, &in) == nil && in.ElementID != "" {
				time.Sleep(150 * time.Millisecond) // actuator latency
				_ = send(in.ElementID, in.Message)
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ws.Close(websocket.StatusNormalClosure, "bye")
		case now := <-tick.C:
			t := now.Sub(start).Seconds()
			for _, e := range d.Elements {
				if e.Kind != "sensor" && e.Kind != "chart" {
					continue
				}
				mid, amp := (e.Max+e.Min)/2, (e.Max-e.Min)/2
				v := mid + amp*0.8*math.Sin(2*math.Pi*t/e.Period) + amp*0.1*(rand.Float64()*2-1)
				v = math.Round(v*100) / 100
				var msg any = map[string]any{"value": v}
				if e.Kind == "chart" {
					msg = map[string]any{"x": now.UTC().Format(time.RFC3339Nano), "y": v}
				}
				if err := send(e.ID, msg); err != nil {
					return err
				}
			}
		}
	}
}
