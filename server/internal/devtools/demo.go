package devtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

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
	// MQTTID is set for devices that talk MQTT (through the dev broker): their
	// external id on the demo MQTT connection.
	MQTTID string `json:"mqtt_id,omitempty"`
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
// With mqttURL set it also creates an MQTT connection to that broker for the
// "Cold room" device (rules, a decoder and a downlink).
func Demo(ctx context.Context, pool *pgxpool.Pool, out, adminUser, adminPass, mqttURL string) error {
	svc := service.New(pool)
	devices, err := store.ListDevices(ctx, pool)
	if err != nil {
		return err
	}
	existing := map[string]uuid.UUID{}
	for _, d := range devices {
		existing[d.Name] = d.ID
	}
	// Keep keys of devices seeded earlier: only missing devices are created.
	var file DemoFile
	if b, err := os.ReadFile(out); err == nil {
		_ = json.Unmarshal(b, &file)
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
		pipeline            string
	}
	plan := []struct {
		name, alg string
		elems     []el
		mqtt      string // external id when the device talks MQTT
	}{
		{"Greenhouse A", "ES256", []el{
			{"Temperature", "sensor", `{"title":"Temperature","unit":"°C","minValue":-10,"maxValue":50}`, 15, 32, 60, 100, ""},
			{"Humidity", "sensor", `{"title":"Humidity","unit":"%","minValue":0,"maxValue":100}`, 40, 85, 90, 100, ""},
			{"Soil moisture", "chart", `{"title":"Soil moisture","unit":"%"}`, 200, 650, 120, 300,
				`[{"kind":"scale","field":"value","mul":0.09775},{"kind":"round","field":"value","decimals":1},{"kind":"clamp","field":"value","min":0,"max":100}]`},
			{"Irrigation pump", "switch", `{"title":"Irrigation pump"}`, 0, 1, 0, 20, ""},
			{"Fan speed", "slider", `{"title":"Fan speed","unit":"%","min":0,"max":100,"step":5}`, 0, 100, 0, 20,
				`[{"kind":"scale","field":"value","mul":0.03333333333333333}]`},
		}, ""},
		{"Boiler room", "RS256", []el{
			{"Water temperature", "sensor", `{"title":"Water temperature","unit":"°C","minValue":0,"maxValue":120}`, 55, 85, 45, 100, ""},
			{"Pressure", "chart", `{"title":"Pressure","unit":"bar"}`, 1.2, 2.4, 30, 300, ""},
			{"Burner", "switch", `{"title":"Burner"}`, 0, 1, 0, 20, ""},
		}, ""},
		// A device that does not speak {"value": N}: one message carries many
		// attributes (nested GPS, its own epoch-ms timestamp), and the relay
		// reports and accepts "ON"/"OFF" strings. Bind widgets to attributes.
		{"Weather station", "ES256", []el{
			{"Climate", "multi", `{"title":"Climate"}`, 0, 0, 120, 200,
				`[{"kind":"script","source":"function transform(msg, ctx) {\n  if (typeof msg.temperature === 'number' && typeof msg.humidity === 'number') {\n    var a = 17.27, b = 237.7;\n    var alpha = ((a * msg.temperature) / (b + msg.temperature)) + Math.log(msg.humidity / 100.0);\n    msg.dew_point = Math.round(((b * alpha) / (a - alpha)) * 10) / 10;\n  }\n  return msg;\n}"}]`},
			{"Gate relay", "relay", `{"title":"Gate relay"}`, 0, 0, 0, 20,
				`[{"kind":"map","field":"value","table":{"OPEN":"ON","CLOSED":"OFF"}}]`},
		}, ""},
		// Talks MQTT to a broker (task start MQTT=1): JSON readings, a binary
		// battery frame decoded by a JavaScript decoder, and a compressor
		// switch commanded over a downlink topic.
		{"Cold room", "ES256", []el{
			{"Cold room temperature", "sensor", `{"title":"Cold room","unit":"°C","minValue":-30,"maxValue":0}`, -23, -16, 80, 100,
				`[{"kind":"deadband","field":"value","abs":0.2,"max_silence":"5m"}]`},
			{"Cold room humidity", "sensor", `{"title":"Humidity","unit":"%","minValue":0,"maxValue":100}`, 72, 90, 110, 100, ""},
			{"Battery", "sensor", `{"title":"Battery","unit":"V","minValue":3,"maxValue":4.2}`, 3.6, 3.9, 900, 50, ""},
			{"Compressor", "switch", `{"title":"Compressor"}`, 0, 1, 0, 20, ""},
		}, "cold-room-1"},
	}
	for _, p := range plan {
		if devID, ok := existing[p.name]; ok {
			elems, err := store.ListElements(ctx, pool, &devID)
			if err == nil {
				elemByName := map[string]store.Element{}
				for _, el := range elems {
					elemByName[el.Name] = el
				}
				for _, e := range p.elems {
					if e.pipeline != "" {
						if target, found := elemByName[e.name]; found {
							_, _ = svc.SaveElementPipeline(ctx, a, target.ID, json.RawMessage(e.pipeline))
						}
					}
				}
			}
			continue
		}
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
		dd := DemoDevice{ID: d.ID.String(), Name: d.Name, Alg: p.alg, PrivateKeyPEM: priv, MQTTID: p.mqtt}
		for _, e := range p.elems {
			created, err := svc.CreateElement(ctx, a, service.ElementInput{DeviceID: d.ID, Name: e.name, Points: e.points,
				Description: "Demo " + e.kind, Details: json.RawMessage(e.details)})
			if err != nil {
				return err
			}
			hint := map[string]string{"multi": "chart", "relay": "switch"}[e.kind]
			if hint == "" {
				hint = e.kind
			}
			if _, err := svc.CreateStyle(ctx, a, store.Style{ElementID: created.ID, Name: "widget",
				Details: json.RawMessage(fmt.Sprintf(`{"widget":%q}`, hint))}); err != nil {
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
			if e.pipeline != "" {
				if _, err := svc.SaveElementPipeline(ctx, a, created.ID, json.RawMessage(e.pipeline)); err != nil {
					return fmt.Errorf("pipeline %s: %w", e.name, err)
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
	if mqttURL != "" {
		if err := demoMQTT(ctx, pool, svc, a, mqttURL, file); err != nil {
			return fmt.Errorf("mqtt demo: %w", err)
		}
	}
	fmt.Printf("demo ready: admin=%s viewer=viewer/viewer12345 (group operators, read-only); %d demo devices, keys in %s\n", adminUser, len(file.Devices), out)
	return nil
}

// Simulate connects every demo device and streams realistic values; it also
// echoes actuator commands back as state (like a real device would).
// transport is websocket, rest, grpc, or mixed (device i uses transport i%3).
// Devices with an MQTT id publish to mqttURL instead (skipped when it's empty).
func Simulate(ctx context.Context, file, wsBase, transport, mqttURL string, log *slog.Logger) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var demo DemoFile
	if err := json.Unmarshal(b, &demo); err != nil {
		return err
	}
	var wg sync.WaitGroup
	i := 0
	for _, d := range demo.Devices {
		if d.MQTTID != "" {
			if mqttURL == "" {
				log.Info("simulator: skipping an MQTT device (no --mqtt-url)", "device", d.Name)
				continue
			}
			wg.Go(func() {
				for ctx.Err() == nil {
					if err := simulateMQTTDevice(ctx, d, mqttURL, log); err != nil && ctx.Err() == nil {
						log.Warn("simulator: MQTT device disconnected, retrying", "device", d.Name, "err", err)
						time.Sleep(3 * time.Second)
					}
				}
			})
			continue
		}
		tr, err := transportFor(transport, i)
		if err != nil {
			return err
		}
		i++
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if err := simulateDevice(ctx, d, wsBase, tr, log); err != nil && ctx.Err() == nil {
					log.Warn("simulator: device disconnected, retrying", "device", d.Name, "err", err)
					time.Sleep(3 * time.Second)
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

func simulateDevice(ctx context.Context, d DemoDevice, base, transport string, log *slog.Logger) error {
	key, err := parsePrivate(d.PrivateKeyPEM)
	if err != nil {
		return err
	}
	method := jwt.GetSigningMethod(d.Alg)
	tok, err := jwt.NewWithClaims(method, jwt.MapClaims{"id": d.ID, "iat": time.Now().Unix(), "exp": time.Now().Add(12 * time.Hour).Unix()}).SignedString(key)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	link, err := dialLink(ctx, transport, base, tok)
	if err != nil {
		return err
	}
	defer link.close()
	log.Info("simulator: connected", "device", d.Name, "transport", transport)

	failed := make(chan error, 1)
	send := func(elementID string, msg any) error { return link.send(ctx, elementID, msg) }
	// Actuators: report initial state, then echo commands back as the new state.
	for _, e := range d.Elements {
		switch e.Kind {
		case "switch":
			_ = send(e.ID, map[string]any{"value": 0})
		case "slider":
			_ = send(e.ID, map[string]any{"value": 900})
		case "relay":
			_ = send(e.ID, map[string]any{"value": "CLOSED"})
		}
	}
	go func() {
		for {
			in, err := link.recv(ctx)
			if err != nil {
				failed <- err
				return
			}
			if shouldEcho(in, d.ID) {
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
			return nil
		case err := <-failed:
			return err
		case now := <-tick.C:
			t := now.Sub(start).Seconds()
			for _, e := range d.Elements {
				if e.Kind == "multi" {
					w := func(mid, amp, period float64) float64 {
						return math.Round((mid+amp*math.Sin(2*math.Pi*t/period)+amp*0.08*(rand.Float64()*2-1))*10) / 10
					}
					if err := send(e.ID, map[string]any{
						"temperature": w(24, 6, e.Period),
						"humidity":    w(55, 15, e.Period*1.3),
						"battery":     math.Round((3.9-0.2*math.Mod(t/600, 1))*100) / 100,
						"ts":          now.UnixMilli(),
						"gps":         map[string]any{"lat": 30.0444, "lng": 31.2357},
					}); err != nil {
						return err
					}
					continue
				}
				if e.Kind != "sensor" && e.Kind != "chart" {
					continue
				}
				mid, amp := (e.Max+e.Min)/2, (e.Max-e.Min)/2
				v := mid + amp*0.8*math.Sin(2*math.Pi*t/e.Period) + amp*0.1*(rand.Float64()*2-1)
				v = math.Round(v*100) / 100
				var msg any = map[string]any{"value": v}
				if e.Kind == "chart" {
					msg = map[string]any{"x": now.UTC().Format(time.RFC3339Nano), "y": v, "value": v}
				}
				if err := send(e.ID, msg); err != nil {
					return err
				}
			}
		}
	}
}

// incomingFrame is what the gateway delivers to a device socket.
type incomingFrame struct {
	ElementID string          `json:"element_id"`
	Message   json.RawMessage `json:"message"`
	Auth      struct {
		UserID json.RawMessage `json:"user_id"`
	} `json:"auth"`
}

// shouldEcho reports whether a frame is a user command the simulated device
// should acknowledge by reporting the new state. Frames from the device
// itself (its other sockets receive them too) must not be echoed: two
// simulators of one device would otherwise ping-pong forever.
func shouldEcho(f incomingFrame, deviceID string) bool {
	if f.ElementID == "" || len(f.Message) == 0 {
		return false
	}
	var sender string
	if json.Unmarshal(f.Auth.UserID, &sender) == nil && sender == deviceID {
		return false
	}
	return true
}
