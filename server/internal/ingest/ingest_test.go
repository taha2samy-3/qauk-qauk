package ingest

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
)

func TestNumericValue(t *testing.T) {
	for in, want := range map[string]any{
		`{"value":42.5}`: 42.5, `{"value":true}`: 1.0, `{"value":false}`: 0.0, `7`: 7.0,
		`{"x":"t","y":3}`: 3.0, `{"value":2,"y":9}`: 2.0, `{"x":"t"}`: nil, `{"value":"12"}`: nil, `null`: nil, `garbage`: nil,
	} {
		got := NumericValue(json.RawMessage(in))
		switch w := want.(type) {
		case nil:
			if got != nil {
				t.Errorf("%s: got %v, want nil", in, *got)
			}
		case float64:
			if got == nil || *got != w {
				t.Errorf("%s: got %v, want %v", in, got, w)
			}
		}
	}
}

func TestToRow(t *testing.T) {
	el, dev := uuid.New(), uuid.New()
	ev, _ := events.New(events.TypeElementMessage, "/quack/gateway/g", el.String(), events.ElementMessage{
		ElementID: el, DeviceID: dev, Source: "device", Actor: events.Actor{ID: dev.String(), Name: "d"},
		Origin: events.Origin{GatewayID: "g", ConnID: "c"}, Message: json.RawMessage(`{"value":3}`),
	})
	b, _ := json.Marshal(ev)
	row, ok := ToRow(b)
	if !ok || row.ElementID != el || row.Value == nil || *row.Value != 3 || row.EventID.String() != ev.ID {
		t.Fatalf("row %+v ok=%v", row, ok)
	}
	if _, ok := ToRow([]byte(`{"type":"io.quack.control.changed.v1"}`)); ok {
		t.Fatal("non-element event converted")
	}
}
