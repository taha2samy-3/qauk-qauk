package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/sourcepipe"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type StrPath struct {
	ID string `path:"id"`
}

type MQTTConnectionIn struct {
	Body events.MQTTConnection
}
type MQTTConnectionUpdateIn struct {
	ID   string `path:"id"`
	Body events.MQTTConnection
}
type MQTTConnectionOut struct{ Body events.MQTTConnection }
type MQTTConnectionsOut struct{ Body []events.MQTTConnection }

// MQTTConnectionDetail is a connection with everything attached to it.
type MQTTConnectionDetail struct {
	events.MQTTConnection
	Uplinks   []events.MQTTUplink        `json:"uplinks"`
	Downlinks []events.MQTTDownlink      `json:"downlinks"`
	Devices   []events.MQTTGrantedDevice `json:"devices"`
	Status    []store.MQTTStatus         `json:"status"`
}
type MQTTConnectionDetailOut struct{ Body MQTTConnectionDetail }

type MQTTUplinkIn struct {
	ID   string `path:"id"`
	Body events.MQTTUplink
}
type MQTTUplinkOut struct{ Body events.MQTTUplink }

type MQTTDownlinkIn struct {
	ID   string `path:"id"`
	Body events.MQTTDownlink
}
type MQTTDownlinkOut struct{ Body events.MQTTDownlink }

type MQTTGrantIn struct {
	ID   string `path:"id"`
	Body struct {
		DeviceID   uuid.UUID `json:"device_id"`
		ExternalID string    `json:"external_id" doc:"How the device appears in MQTT topics or payloads"`
	}
}
type MQTTRevokeIn struct {
	ID       string    `path:"id"`
	DeviceID uuid.UUID `path:"device_id"`
}

type MQTTCaptureIn struct {
	ID   string `path:"id"`
	Body struct {
		Seconds int `json:"seconds" minimum:"0" maximum:"600" doc:"Capture for this long (0 = stop)"`
	}
}
type MQTTCaptureOut struct {
	Body struct {
		CaptureUntil *time.Time `json:"capture_until"`
	}
}
type MQTTCapturedOut struct{ Body []events.MQTTCaptured }
type MQTTRejectedIn struct {
	Limit int `query:"limit" minimum:"1" maximum:"500" default:"100"`
}
type MQTTRejectedOut struct{ Body []events.MQTTRejected }
type MQTTStatusOut struct{ Body []store.MQTTStatus }

type DecoderIn struct {
	Body struct {
		Name   string `json:"name" minLength:"1" maxLength:"100"`
		Source string `json:"source" doc:"JavaScript with decodeUplink(input) and/or encodeDownlink(input) (TTN/ChirpStack contract), ≤ 40 KB"`
	}
}
type DecoderUpdateIn struct {
	ID   string `path:"id"`
	Body struct {
		Name   string `json:"name" minLength:"1" maxLength:"100"`
		Source string `json:"source"`
	}
}
type DecoderOut struct{ Body events.MQTTDecoder }
type DecodersOut struct{ Body []events.MQTTDecoder }

// MQTTTestIn runs the source pipeline on a sample, without publishing.
type MQTTTestIn struct {
	Body struct {
		Format        string            `json:"format" enum:"json,text,number,bytes" default:"json"`
		Device        json.RawMessage   `json:"device"`
		FieldMap      json.RawMessage   `json:"field_map"`
		Time          *string           `json:"time,omitempty"`
		DecoderSource string            `json:"decoder_source,omitempty" doc:"Decoder to try (or decoder_id)"`
		DecoderID     string            `json:"decoder_id,omitempty"`
		Topic         string            `json:"topic"`
		Payload       string            `json:"payload" doc:"Text, or hex:… / base64:… for binary"`
		UserProps     map[string]string `json:"user_properties,omitempty"`
	}
}
type MQTTTestValue struct {
	Device  string          `json:"device_external_id"`
	Element string          `json:"element"`
	Message json.RawMessage `json:"message"`
	TS      *time.Time      `json:"ts,omitempty"`
}
type MQTTTestOut struct {
	Body struct {
		Values     []MQTTTestValue `json:"values"`
		Rejections []string        `json:"rejections"`
		Fatal      bool            `json:"fatal"`
	}
}

func (a *API) registerAdminMQTT(api huma.API) {
	tags := adminTags("mqtt")
	admin := func(ctx context.Context) (service.Actor, error) {
		u, err := requireAdmin(ctx)
		if err != nil {
			return service.Actor{}, err
		}
		return service.UserActor(u), nil
	}

	// --- connections ---
	huma.Register(api, huma.Operation{OperationID: "list-mqtt-connections", Method: http.MethodGet, Path: "/api/v1/admin/mqtt/connections", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*MQTTConnectionsOut, error) {
			if _, err := admin(ctx); err != nil {
				return nil, err
			}
			cs, err := store.ListMQTTConnections(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &MQTTConnectionsOut{Body: cs}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-mqtt-connection", Method: http.MethodPost, Path: "/api/v1/admin/mqtt/connections", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *MQTTConnectionIn) (*MQTTConnectionOut, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			c, err := a.svc.CreateMQTTConnection(ctx, act, in.Body)
			if err != nil {
				return nil, a.fail(err)
			}
			return &MQTTConnectionOut{Body: c}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-mqtt-connection", Method: http.MethodGet, Path: "/api/v1/admin/mqtt/connections/{id}", Tags: tags},
		func(ctx context.Context, in *StrPath) (*MQTTConnectionDetailOut, error) {
			if _, err := admin(ctx); err != nil {
				return nil, err
			}
			cfg, err := store.MQTTConfig(ctx, a.pool, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			st, err := store.ListMQTTStatus(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			st = slices.DeleteFunc(st, func(s store.MQTTStatus) bool { return s.ConnectionID != in.ID })
			return &MQTTConnectionDetailOut{Body: MQTTConnectionDetail{MQTTConnection: cfg.Connection, Uplinks: cfg.Uplinks,
				Downlinks: cfg.Downlinks, Devices: cfg.Devices, Status: st}}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-mqtt-connection", Method: http.MethodPut, Path: "/api/v1/admin/mqtt/connections/{id}", Tags: tags},
		func(ctx context.Context, in *MQTTConnectionUpdateIn) (*MQTTConnectionOut, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			c, err := a.svc.UpdateMQTTConnection(ctx, act, in.ID, in.Body)
			if err != nil {
				return nil, a.fail(err)
			}
			return &MQTTConnectionOut{Body: c}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-mqtt-connection", Method: http.MethodDelete, Path: "/api/v1/admin/mqtt/connections/{id}", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *StrPath) (*struct{}, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteMQTTConnection(ctx, act, in.ID))
		})
	huma.Register(api, huma.Operation{OperationID: "list-mqtt-status", Method: http.MethodGet, Path: "/api/v1/admin/mqtt/status", Tags: tags,
		Summary: "Connection state per slot, as reported by the gateways that own them"},
		func(ctx context.Context, _ *struct{}) (*MQTTStatusOut, error) {
			if _, err := admin(ctx); err != nil {
				return nil, err
			}
			st, err := store.ListMQTTStatus(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &MQTTStatusOut{Body: st}, nil
		})

	// --- granted devices ---
	huma.Register(api, huma.Operation{OperationID: "grant-mqtt-device", Method: http.MethodPut, Path: "/api/v1/admin/mqtt/connections/{id}/devices", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *MQTTGrantIn) (*struct{}, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.GrantMQTTDevice(ctx, act, in.ID, in.Body.DeviceID, in.Body.ExternalID))
		})
	huma.Register(api, huma.Operation{OperationID: "revoke-mqtt-device", Method: http.MethodDelete, Path: "/api/v1/admin/mqtt/connections/{id}/devices/{device_id}", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *MQTTRevokeIn) (*struct{}, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.RevokeMQTTDevice(ctx, act, in.ID, in.DeviceID))
		})

	// --- uplinks ---
	huma.Register(api, huma.Operation{OperationID: "create-mqtt-uplink", Method: http.MethodPost, Path: "/api/v1/admin/mqtt/connections/{id}/uplinks", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *MQTTUplinkIn) (*MQTTUplinkOut, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			u, err := a.svc.CreateMQTTUplink(ctx, act, in.ID, in.Body)
			if err != nil {
				return nil, a.fail(err)
			}
			return &MQTTUplinkOut{Body: u}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-mqtt-uplink", Method: http.MethodDelete, Path: "/api/v1/admin/mqtt/uplinks/{id}", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *StrPath) (*struct{}, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteMQTTUplink(ctx, act, in.ID))
		})
	huma.Register(api, huma.Operation{OperationID: "capture-mqtt-uplink", Method: http.MethodPost, Path: "/api/v1/admin/mqtt/uplinks/{id}/capture", Tags: tags,
		Summary: "Copy received messages of this rule to mqtt-capture.v1 for up to 10 minutes"},
		func(ctx context.Context, in *MQTTCaptureIn) (*MQTTCaptureOut, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			until, err := a.svc.CaptureMQTTUplink(ctx, act, in.ID, time.Duration(in.Body.Seconds)*time.Second)
			if err != nil {
				return nil, a.fail(err)
			}
			out := &MQTTCaptureOut{}
			out.Body.CaptureUntil = until
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "list-mqtt-captured", Method: http.MethodGet, Path: "/api/v1/admin/mqtt/uplinks/{id}/captured", Tags: tags,
		Summary: "The newest captured messages of a rule (last hour, at most 20)"},
		func(ctx context.Context, in *StrPath) (*MQTTCapturedOut, error) {
			if _, err := admin(ctx); err != nil {
				return nil, err
			}
			recs, err := a.readTopic(ctx, events.TopicMQTTCapture, 2000)
			if err != nil {
				return nil, a.fail(err)
			}
			out := []events.MQTTCaptured{}
			for _, raw := range recs {
				var c events.MQTTCaptured
				if json.Unmarshal(raw, &c) == nil && c.UplinkID == in.ID {
					out = append(out, c)
				}
			}
			slices.SortFunc(out, func(x, y events.MQTTCaptured) int { return y.Time.Compare(x.Time) })
			return &MQTTCapturedOut{Body: out[:min(len(out), 20)]}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "list-mqtt-rejected", Method: http.MethodGet, Path: "/api/v1/admin/mqtt/rejected", Tags: tags,
		Summary: "The newest rejected MQTT messages (mqtt.dlq.v1)"},
		func(ctx context.Context, in *MQTTRejectedIn) (*MQTTRejectedOut, error) {
			if _, err := admin(ctx); err != nil {
				return nil, err
			}
			recs, err := a.readTopic(ctx, events.TopicMQTTDLQ, int64(in.Limit))
			if err != nil {
				return nil, a.fail(err)
			}
			out := []events.MQTTRejected{}
			for _, raw := range recs {
				var r events.MQTTRejected
				if json.Unmarshal(raw, &r) == nil {
					out = append(out, r)
				}
			}
			slices.SortFunc(out, func(x, y events.MQTTRejected) int { return y.Time.Compare(x.Time) })
			return &MQTTRejectedOut{Body: out[:min(len(out), in.Limit)]}, nil
		})

	// --- downlinks ---
	huma.Register(api, huma.Operation{OperationID: "create-mqtt-downlink", Method: http.MethodPost, Path: "/api/v1/admin/mqtt/connections/{id}/downlinks", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *MQTTDownlinkIn) (*MQTTDownlinkOut, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			d, err := a.svc.CreateMQTTDownlink(ctx, act, in.ID, in.Body)
			if err != nil {
				return nil, a.fail(err)
			}
			return &MQTTDownlinkOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-mqtt-downlink", Method: http.MethodDelete, Path: "/api/v1/admin/mqtt/downlinks/{id}", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *StrPath) (*struct{}, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteMQTTDownlink(ctx, act, in.ID))
		})

	// --- decoders ---
	huma.Register(api, huma.Operation{OperationID: "list-decoders", Method: http.MethodGet, Path: "/api/v1/admin/mqtt/decoders", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*DecodersOut, error) {
			if _, err := admin(ctx); err != nil {
				return nil, err
			}
			ds, err := store.ListDecoders(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DecodersOut{Body: ds}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-decoder", Method: http.MethodPost, Path: "/api/v1/admin/mqtt/decoders", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *DecoderIn) (*DecoderOut, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			d, err := a.svc.CreateDecoder(ctx, act, in.Body.Name, in.Body.Source)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DecoderOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-decoder", Method: http.MethodPut, Path: "/api/v1/admin/mqtt/decoders/{id}", Tags: tags,
		Description: "Stores a new version and republishes every connection using it."},
		func(ctx context.Context, in *DecoderUpdateIn) (*DecoderOut, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			d, err := a.svc.UpdateDecoder(ctx, act, in.ID, in.Body.Name, in.Body.Source)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DecoderOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-decoder", Method: http.MethodDelete, Path: "/api/v1/admin/mqtt/decoders/{id}", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *StrPath) (*struct{}, error) {
			act, err := admin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteDecoder(ctx, act, in.ID))
		})

	// --- test the source pipeline ---
	huma.Register(api, huma.Operation{OperationID: "test-mqtt-pipeline", Method: http.MethodPost, Path: "/api/v1/admin/mqtt/test", Tags: tags,
		Summary: "Run a sample message through a decoder and field map, without publishing (same sandbox as the gateways)"},
		func(ctx context.Context, in *MQTTTestIn) (*MQTTTestOut, error) {
			if _, err := admin(ctx); err != nil {
				return nil, err
			}
			b := in.Body
			src := b.DecoderSource
			if src == "" && b.DecoderID != "" {
				d, err := store.GetDecoder(ctx, a.pool, b.DecoderID)
				if err != nil {
					return nil, a.fail(err)
				}
				src = d.Source
			}
			decs := map[string]*sourcepipe.Decoder{}
			u := events.MQTTUplink{Format: b.Format, Device: b.Device, FieldMap: b.FieldMap, Time: b.Time}
			if src != "" {
				dec, err := sourcepipe.Compile(src)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("decoder: " + err.Error())
				}
				id := "test"
				u.DecoderID, decs[id] = &id, dec
			}
			rule, err := sourcepipe.CompileRule(u, decs)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			payload, err := decodeSample(b.Payload)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("payload: " + err.Error())
			}
			vals, rej := sourcepipe.Process(rule, sourcepipe.Message{Topic: b.Topic, Payload: payload, UserProperties: b.UserProps})
			out := &MQTTTestOut{}
			out.Body.Values = make([]MQTTTestValue, len(vals))
			for i, v := range vals {
				out.Body.Values[i] = MQTTTestValue{Device: v.DeviceExternalID, Element: v.Element, Message: v.Message, TS: v.TS}
			}
			out.Body.Rejections = []string{}
			for _, r := range rej {
				out.Body.Rejections = append(out.Body.Rejections, r.Reason)
				out.Body.Fatal = out.Body.Fatal || r.Fatal
			}
			return out, nil
		})
}

func decodeSample(s string) ([]byte, error) {
	switch {
	case strings.HasPrefix(s, "hex:"):
		return hex.DecodeString(strings.ReplaceAll(s[4:], " ", ""))
	case strings.HasPrefix(s, "base64:"):
		return base64.StdEncoding.DecodeString(s[7:])
	}
	return []byte(s), nil
}

// readTopic returns record values from the tail of a topic.
func (a *API) readTopic(ctx context.Context, topic string, perPartition int64) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	recs, err := bus.ReadTail(ctx, a.cfg.KafkaBrokers, topic, perPartition)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(recs))
	for i, r := range recs {
		out[i] = r.Value
	}
	return out, nil
}
