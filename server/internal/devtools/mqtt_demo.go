package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// The demo's MQTT side: a connection to the dev broker serving the Cold room
// device, and a simulated device publishing to that broker like real hardware.

const (
	demoMQTTName  = "Demo broker"
	demoTopicBase = "quack/demo"
)

// demoBatteryDecoder decodes the Cold room's binary frame: [battery × 10, flags].
const demoBatteryDecoder = `// Cold room binary frame: byte 0 = battery × 10, byte 1 bit 0 = door open.
function decodeUplink(input) {
  if (input.bytes.length < 2) {
    return { errors: ["frame too short"] };
  }
  return {
    data: { battery: input.bytes[0] / 10, door_open: (input.bytes[1] & 1) === 1 },
    warnings: [],
    errors: []
  };
}`

func demoMQTT(ctx context.Context, pool *pgxpool.Pool, svc *service.Service, a service.Actor, mqttURL string, file DemoFile) error {
	conns, err := store.ListMQTTConnections(ctx, pool)
	if err != nil {
		return err
	}
	for _, c := range conns {
		if c.Name == demoMQTTName {
			return nil // already there (demo is additive)
		}
	}
	var dev *DemoDevice
	for i := range file.Devices {
		if file.Devices[i].MQTTID != "" {
			dev = &file.Devices[i]
		}
	}
	if dev == nil {
		return nil
	}
	svc.AllowInsecureTLS = true // the dev broker is plain TCP
	conn, err := svc.CreateMQTTConnection(ctx, a, events.MQTTConnection{Name: demoMQTTName, BrokerURL: mqttURL,
		ClientIDPrefix: "quack-demo", Enabled: true})
	if err != nil {
		return err
	}
	devID, err := uuid.Parse(dev.ID)
	if err != nil {
		return err
	}
	if err := svc.GrantMQTTDevice(ctx, a, conn.ID, devID, dev.MQTTID); err != nil {
		return err
	}
	dec, err := svc.CreateDecoder(ctx, a, "Cold room battery frame", demoBatteryDecoder)
	if err != nil {
		return err
	}
	rules := []events.MQTTUplink{
		{TopicFilter: demoTopicBase + "/+/up", QoS: 1, Format: "json", Device: json.RawMessage(`{"segment":2}`), Enabled: true,
			FieldMap: json.RawMessage(`[{"element":"Cold room temperature","value":"t"},{"element":"Cold room humidity","value":"h"}]`)},
		{TopicFilter: demoTopicBase + "/+/bin", QoS: 1, Format: "bytes", DecoderID: &dec.ID, Device: json.RawMessage(`{"segment":2}`), Enabled: true,
			FieldMap: json.RawMessage(`[{"element":"Battery","value":"battery"}]`)},
		{TopicFilter: demoTopicBase + "/+/state", QoS: 1, Format: "json", Device: json.RawMessage(`{"segment":2}`), Enabled: true,
			FieldMap: json.RawMessage(`[{"element":"Compressor","value":"compressor"}]`)},
	}
	for _, r := range rules {
		if _, err := svc.CreateMQTTUplink(ctx, a, conn.ID, r); err != nil {
			return err
		}
	}
	ct := "application/json"
	_, err = svc.CreateMQTTDownlink(ctx, a, conn.ID, events.MQTTDownlink{DeviceExternalID: dev.MQTTID, Element: "Compressor",
		TopicTemplate: demoTopicBase + "/{device}/cmd/{element}", QoS: 1, ContentType: &ct,
		Encoder: json.RawMessage(`{"template":{"compressor":"{{value}}"}}`)})
	return err
}

// simulateMQTTDevice publishes the Cold room's readings to the broker and
// applies compressor commands from its cmd topic, reporting the new state.
func simulateMQTTDevice(ctx context.Context, d DemoDevice, mqttURL string, log *slog.Logger) error {
	u, err := url.Parse(mqttURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	base := demoTopicBase + "/" + d.MQTTID
	var stateMu sync.Mutex
	state := 0.0
	var cm *autopaho.ConnectionManager
	publish := func(topic string, payload []byte) error {
		pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
		defer pcancel()
		_, err := cm.Publish(pctx, &paho.Publish{Topic: topic, QoS: 1, Payload: payload})
		return err
	}
	failed := make(chan error, 1)
	cm, err = autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls: []*url.URL{u}, KeepAlive: 30, CleanStartOnInitialConnection: true,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			go func() {
				sctx, scancel := context.WithTimeout(ctx, 10*time.Second)
				defer scancel()
				_ = cm.AwaitConnection(sctx)
				if _, err := cm.Subscribe(sctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: base + "/cmd/#", QoS: 1}}}); err != nil {
					failed <- err
				}
			}()
		},
		ClientConfig: paho.ClientConfig{
			ClientID: "demo-" + d.MQTTID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(pr paho.PublishReceived) (bool, error) {
				if !strings.HasPrefix(pr.Packet.Topic, base+"/cmd/") {
					return true, nil
				}
				var cmd struct {
					Compressor any `json:"compressor"`
				}
				if json.Unmarshal(pr.Packet.Payload, &cmd) == nil {
					go func() {
						time.Sleep(150 * time.Millisecond) // actuator latency
						stateMu.Lock()
						if f, ok := cmd.Compressor.(float64); ok {
							state = f
						}
						v := state
						stateMu.Unlock()
						_ = publish(base+"/state", fmt.Appendf(nil, `{"compressor":%v}`, v))
					}()
				}
				return true, nil
			}},
		},
	})
	if err != nil {
		return err
	}
	actx, acancel := context.WithTimeout(ctx, 10*time.Second)
	err = cm.AwaitConnection(actx)
	acancel()
	if err != nil {
		return err
	}
	log.Info("simulator: connected", "device", d.Name, "transport", "mqtt", "broker", mqttURL)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	start := time.Now()
	n := 0
	for {
		select {
		case <-ctx.Done():
			dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = cm.Disconnect(dctx)
			dcancel()
			return nil
		case err := <-failed:
			return err
		case now := <-tick.C:
			t := now.Sub(start).Seconds()
			temp := -19.5 + 3*math.Sin(2*math.Pi*t/80) + 0.3*(rand.Float64()*2-1)
			hum := 81 + 7*math.Sin(2*math.Pi*t/110) + rand.Float64()
			if err := publish(base+"/up", fmt.Appendf(nil, `{"t":%.2f,"h":%.1f,"ts":%d}`, temp, hum, now.UnixMilli())); err != nil {
				return err
			}
			if n%30 == 0 { // periodic state report, as real devices do (also covers a missed first one)
				stateMu.Lock()
				v := state
				stateMu.Unlock()
				if err := publish(base+"/state", fmt.Appendf(nil, `{"compressor":%v}`, v)); err != nil {
					return err
				}
			}
			if n%5 == 0 { // binary frame for the JavaScript decoder
				battery := 3.9 - 0.2*math.Mod(t/900, 1)
				door := byte(0)
				if rand.IntN(10) == 0 {
					door = 1
				}
				if err := publish(base+"/bin", []byte{byte(math.Round(battery * 10)), door}); err != nil {
					return err
				}
			}
			n++
		}
	}
}
