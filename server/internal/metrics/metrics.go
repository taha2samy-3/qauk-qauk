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

// MQTT transport (gateway role mqtt).
var (
	MQTTConnected = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "quack_mqtt_connected", Help: "1 while this gateway's client for a connection slot is connected.",
	}, []string{"connection", "slot"})
	MQTTOwnedSlots = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "quack_mqtt_owned_slots", Help: "MQTT connection slots this gateway owns (HRW).",
	})
	MQTTMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_mqtt_messages_received_total", Help: "MQTT messages received from brokers.",
	}, []string{"connection"})
	MQTTValues = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_mqtt_values_published_total", Help: "Element values published from MQTT messages.",
	}, []string{"connection"})
	MQTTRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_mqtt_rejected_total", Help: "MQTT messages (or values) rejected, by reason.",
	}, []string{"reason"})
	MQTTDecoderSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "quack_mqtt_decoder_seconds", Help: "Source pipeline time per message.",
		Buckets: []float64{0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025},
	})
	MQTTUnacked = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "quack_mqtt_unacked", Help: "QoS 1 messages waiting for their values to be durable.",
	})
	MQTTDownlinks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_mqtt_downlinks_total", Help: "Commands published to MQTT, by result.",
	}, []string{"result"})
)
