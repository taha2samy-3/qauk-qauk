package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	WebhookDeliveries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_webhook_deliveries_total", Help: "Webhook delivery attempts by result: delivered, retry (will be tried again), failed (given up).",
	}, []string{"result"})
	WebhookQueue = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "quack_webhook_queue", Help: "Webhook deliveries not yet delivered or failed.",
	})
	AlertTransitions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "quack_alert_transitions_total", Help: "Alert rules that started firing or resolved.",
	}, []string{"state"})
	AlertErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "quack_alert_errors_total", Help: "Alert evaluation batches that failed (retried).",
	})
)
