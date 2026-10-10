//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func dlqFrom(t *testing.T) func(match func(map[string]any) bool) map[string]any {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatal(err)
	}
	topic := bus.TopicName(events.TopicElementPipelineDLQ)
	ends, err := kadm.NewClient(cl).ListEndOffsets(context.Background(), topic)
	cl.Close()
	if err != nil {
		t.Fatal(err)
	}
	from := map[string]map[int32]kgo.Offset{topic: {}}
	ends.Each(func(o kadm.ListedOffset) { from[topic][o.Partition] = kgo.NewOffset().At(o.Offset) })
	return func(match func(map[string]any) bool) map[string]any {
		rd, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumePartitions(from), kgo.FetchMaxWait(200*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		defer rd.Close()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			fs := rd.PollFetches(ctx)
			cancel()
			var hit map[string]any
			fs.EachRecord(func(r *kgo.Record) {
				var rec map[string]any
				if hit == nil && json.Unmarshal(r.Value, &rec) == nil && match(rec) {
					hit = rec
				}
			})
			if hit != nil {
				return hit
			}
		}
		return nil
	}
}

func TestPipelineLiveDistributionAndRollback(t *testing.T) {
	gw1 := startInstance(t, "gw-pipe-1", false)
	gw2 := startInstance(t, "gw-pipe-2", false)

	mkUser(t, "pipe-admin", true)
	c1 := newClient(t, gw1.URL)
	c1.login("pipe-admin", "pipe-admin-password")

	// Create device and element
	fix := setupRealtime(t, "pipe-dist", 100)
	authHdr := deviceHeader(t, fix.key, fix.device.ID.String()).Get("Authorization")

	// Listen to bus events
	evWait := eventsFrom(t)

	// 1. Initial message without pipeline
	r0 := c1.do(http.MethodPost, "/device/v1/messages", []any{
		map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": 10.0}},
	}, "Authorization", authHdr)
	if r0.Status != http.StatusOK {
		t.Fatalf("send without pipeline: %d %s", r0.Status, r0.Body)
	}

	ev0 := evWait(func(e *events.Event) bool {
		var em events.ElementMessage
		return e.DecodeData(&em) == nil && em.ElementID == fix.elem.ID
	})
	if ev0 == nil || ev0.QuackPipeline != nil {
		t.Fatalf("expected ev0 with no pipeline attribute, got %v", ev0)
	}

	// 2. Save pipeline v1 (scale x2) via API on gw1
	scaleV1 := `[{"kind":"scale","field":"value","mul":2.0}]`
	c1.must(http.StatusOK, http.MethodPut, fmt.Sprintf("/api/v1/admin/elements/%s/pipeline", fix.elem.ID),
		map[string]any{"steps": json.RawMessage(scaleV1)}, nil)

	// Allow outbox and Redpanda compaction to propagate snapshot to both gateways
	time.Sleep(1500 * time.Millisecond)

	// Send to gw2: verify gw2 picked up v1 live through device-config.v1!
	c2 := newClient(t, gw2.URL)
	r1 := c2.do(http.MethodPost, "/device/v1/messages", []any{
		map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": 15.0}},
	}, "Authorization", authHdr)
	if r1.Status != http.StatusOK {
		t.Fatalf("send v1 to gw2: %d %s", r1.Status, r1.Body)
	}

	ev1 := evWait(func(e *events.Event) bool {
		var em events.ElementMessage
		if e.DecodeData(&em) == nil && em.ElementID == fix.elem.ID {
			var msg map[string]any
			_ = json.Unmarshal(em.Message, &msg)
			return msg["value"] == 30.0 // 15 * 2
		}
		return false
	})
	if ev1 == nil {
		t.Fatal("expected transformed event on gw2 with value 30.0")
	}
	if ev1.QuackPipeline == nil || *ev1.QuackPipeline != 1 {
		t.Fatalf("expected quackpipeline=1, got %v", ev1.QuackPipeline)
	}

	// 3. Update to v2 (scale x3)
	scaleV2 := `[{"kind":"scale","field":"value","mul":3.0}]`
	c1.must(http.StatusOK, http.MethodPut, fmt.Sprintf("/api/v1/admin/elements/%s/pipeline", fix.elem.ID),
		map[string]any{"steps": json.RawMessage(scaleV2)}, nil)
	time.Sleep(1500 * time.Millisecond)

	// 4. Rollback to v1 via API
	c1.must(http.StatusOK, http.MethodPost, fmt.Sprintf("/api/v1/admin/elements/%s/pipeline/rollback", fix.elem.ID),
		map[string]any{"version": 1}, nil)
	time.Sleep(1500 * time.Millisecond)

	// Send to gw2 again: verify rolled back to x2
	c2.do(http.MethodPost, "/device/v1/messages", []any{
		map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": 20.0}},
	}, "Authorization", authHdr)

	evRollback := evWait(func(e *events.Event) bool {
		var em events.ElementMessage
		if e.DecodeData(&em) == nil && em.ElementID == fix.elem.ID {
			var msg map[string]any
			_ = json.Unmarshal(em.Message, &msg)
			return msg["value"] == 40.0 // 20 * 2 (version 3 is rollback of version 1)
		}
		return false
	})
	if evRollback == nil {
		t.Fatal("expected rolled back event on gw2 with value 40.0")
	}
	if evRollback.QuackPipeline == nil || *evRollback.QuackPipeline != 3 {
		t.Fatalf("expected quackpipeline=3 (rollback version), got %v", evRollback.QuackPipeline)
	}
}

func TestPipelineDeadbandAcrossGateways(t *testing.T) {
	gw1 := startInstance(t, "gw-deadband-1", false)
	gw2 := startInstance(t, "gw-deadband-2", false)

	mkUser(t, "db-admin", true)
	c1 := newClient(t, gw1.URL)
	c1.login("db-admin", "db-admin-password")

	fix := setupRealtime(t, "pipe-deadband", 100)
	authHdr := deviceHeader(t, fix.key, fix.device.ID.String()).Get("Authorization")

	// Configure deadband step: abs: 5.0
	deadbandSteps := `[{"kind":"deadband","field":"value","abs":5.0,"max_silence":"10m"}]`
	c1.must(http.StatusOK, http.MethodPut, fmt.Sprintf("/api/v1/admin/elements/%s/pipeline", fix.elem.ID),
		map[string]any{"steps": json.RawMessage(deadbandSteps)}, nil)
	time.Sleep(1500 * time.Millisecond)

	evWait := eventsFrom(t)

	// 1. First reading to gw1: value 10.0 -> accepted
	r1 := c1.do(http.MethodPost, "/device/v1/messages", []any{
		map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": 10.0}},
	}, "Authorization", authHdr)
	if r1.Status != http.StatusOK {
		t.Fatalf("send to gw1: %d %s", r1.Status, r1.Body)
	}

	ev1 := evWait(func(e *events.Event) bool {
		var em events.ElementMessage
		return e.DecodeData(&em) == nil && em.ElementID == fix.elem.ID
	})
	if ev1 == nil {
		t.Fatal("expected published event for first reading on gw1")
	}

	// Wait for event broadcast on element-events.v1 to reach gw2's hub.Latest
	time.Sleep(1000 * time.Millisecond)

	// 2. Device moves to gw2 and sends delta 2.0 (12.0 - 10.0 < 5.0) -> filtered!
	c2 := newClient(t, gw2.URL)
	r2 := c2.do(http.MethodPost, "/device/v1/messages", []any{
		map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": 12.0}},
	}, "Authorization", authHdr)
	if r2.Status != http.StatusOK {
		t.Fatalf("send to gw2: %d %s", r2.Status, r2.Body)
	}
	var res2 struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	r2.JSON(t, &res2)
	if len(res2.Results) != 1 || res2.Results[0].Status != "filtered" {
		t.Fatalf("expected filtered status on gw2 using hub.Latest from gw1, got %+v", res2)
	}

	// 3. Device sends delta 10.0 (20.0 - 10.0 >= 5.0) -> accepted!
	r3 := c2.do(http.MethodPost, "/device/v1/messages", []any{
		map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": 20.0}},
	}, "Authorization", authHdr)
	if r3.Status != http.StatusOK {
		t.Fatalf("send passing delta to gw2: %d %s", r3.Status, r3.Body)
	}
	var res3 struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	r3.JSON(t, &res3)
	if len(res3.Results) != 1 || res3.Results[0].Status != "accepted" {
		t.Fatalf("expected accepted status on gw2, got %+v", res3)
	}
}

func TestPipelinePreviewAndDLQ(t *testing.T) {
	inst := startInstance(t, "gw-prev-dlq", true)

	mkUser(t, "prev-admin", true)
	c := newClient(t, inst.URL)
	c.login("prev-admin", "prev-admin-password")

	fix := setupRealtime(t, "pipe-prev", 100)
	// 1. Seed historical events
	now := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	rows := make([]history.Event, 5)
	for i := 0; i < 5; i++ {
		rows[i] = history.Event{
			Time:      now.Add(time.Duration(i) * time.Minute),
			ID:        uuid.Must(uuid.NewV7()),
			ElementID: fix.elem.ID,
			DeviceID:  fix.device.ID,
			Source:    "device",
			Payload:   json.RawMessage(fmt.Sprintf(`{"value": %d}`, (i+1)*10)),
		}
	}
	if _, err := hist.Append(context.Background(), rows); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	// 2. Call preview on the stored messages
	scaleSteps := `[{"kind":"scale","field":"value","mul":2.5}]`
	var prevRes struct {
		Summary struct {
			Total  int `json:"total"`
			Passed int `json:"passed"`
		} `json:"summary"`
		Rows []struct {
			Time   string          `json:"time"`
			Before json.RawMessage `json:"before"`
			After  json.RawMessage `json:"after"`
		} `json:"rows"`
	}
	c.must(http.StatusOK, http.MethodPost, fmt.Sprintf("/api/v1/admin/elements/%s/pipeline/preview", fix.elem.ID),
		map[string]any{"steps": json.RawMessage(scaleSteps)}, &prevRes)

	if prevRes.Summary.Total != 5 || prevRes.Summary.Passed != 5 {
		t.Fatalf("expected 5 total/passed in preview summary, got %+v", prevRes.Summary)
	}
	if len(prevRes.Rows) != 5 {
		t.Fatalf("expected 5 preview rows, got %d", len(prevRes.Rows))
	}

	// 3. Configure pipeline and test DLQ on failure
	dlqWait := dlqFrom(t)

	c.must(http.StatusOK, http.MethodPut, fmt.Sprintf("/api/v1/admin/elements/%s/pipeline", fix.elem.ID),
		map[string]any{"steps": json.RawMessage(scaleSteps)}, nil)
	time.Sleep(1500 * time.Millisecond)

	authHdr := deviceHeader(t, fix.key, fix.device.ID.String()).Get("Authorization")

	// Send invalid string to scale step -> trigger pipeline_failed and DLQ write
	rBad := c.do(http.MethodPost, "/device/v1/messages", []any{
		map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": "not-numeric"}},
	}, "Authorization", authHdr)
	if rBad.Status != http.StatusOK {
		t.Fatalf("expected 200 response with failed status, got %d", rBad.Status)
	}
	var badRes struct {
		Results []struct {
			Status string `json:"status"`
			Code   string `json:"code"`
		} `json:"results"`
	}
	rBad.JSON(t, &badRes)
	if len(badRes.Results) != 1 || badRes.Results[0].Status != "pipeline_failed" || badRes.Results[0].Code != "pipeline_failed" {
		t.Fatalf("expected pipeline_failed in response, got %+v", badRes)
	}

	// Verify element-pipeline.dlq.v1 receives the failure record
	dlqHit := dlqWait(func(rec map[string]any) bool {
		return rec["element_id"] == fix.elem.ID.String() && rec["device_id"] == fix.device.ID.String()
	})
	if dlqHit == nil {
		t.Fatal("expected DLQ event on element-pipeline.dlq.v1")
	}
	if dlqHit["step"] != "scale" {
		t.Fatalf("expected DLQ step=scale, got %v", dlqHit["step"])
	}
	_ = uuid.Nil
	_ = store.Element{}
}
