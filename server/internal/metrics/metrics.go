// Package metrics declares the Prometheus metrics exposed on /metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	WSConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "quack_ws_connections", Help: "Open WebSocket connections.",
	}, []string{"kind"})
	WSRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_ws_rejected_total", Help: "Rejected WebSocket handshakes.",
	}, []string{"kind", "reason"})
	MessagesIn = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_messages_in_total", Help: "Frames received from clients.",
	}, []string{"kind"})
	FramesOut = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_frames_out_total", Help: "Frames queued to clients.",
	}, []string{"kind"})
	Dropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_dropped_total", Help: "Frames or connections dropped.",
	}, []string{"reason"})
	BusProduceErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_bus_produce_errors_total", Help: "Failed async produces.",
	}, []string{"topic"})
	OutboxPublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "quack_outbox_published_total", Help: "Outbox rows relayed to the bus.",
	})
	IngestRows = promauto.NewCounter(prometheus.CounterOpts{
		Name: "quack_ingest_rows_total", Help: "Element events written to the history store.",
	})
	IngestDeadLetters = promauto.NewCounter(prometheus.CounterOpts{
		Name: "quack_ingest_dead_letters_total", Help: "Element events the history store rejected (sent to element-events.dlq.v1).",
	})
	IngestLag = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "quack_ingest_lag_seconds", Help: "Age of the newest event in the last ingested batch.",
	})
)

func init() {
	// Pre-create label values so the series exist before the first event.
	for _, k := range []string{"device", "browser"} {
		WSConnections.WithLabelValues(k)
		MessagesIn.WithLabelValues(k)
		FramesOut.WithLabelValues(k)
	}
}
