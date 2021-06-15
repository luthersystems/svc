/*
Helper library to register prometheus metrics for logrus errors.
Inspired by Matthias Friedrich's blog post:
https://blog.mafr.de/2019/03/03/monitoring-log-statements-in-go/
*/
package logmon

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	log "github.com/sirupsen/logrus"
)

// NewPrometheusHook creates prometheus metrics.
func NewPrometheusHook() *PrometheusHook {
	levelCounter := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "log_statements_total",
			Help: "Number of log statements, differentiated by log level.",
		},
		[]string{"level"},
	)

	msgCounter := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "log_statements_message",
			Help: "Number of log statements, differentiated by log level and message.",
		},
		[]string{"level", "message"},
	)

	return &PrometheusHook{
		lcounter: levelCounter,
		mcounter: msgCounter,
	}
}

// PrometheusHook tracks log metrics.
type PrometheusHook struct {
	lcounter *prometheus.CounterVec
	mcounter *prometheus.CounterVec
}

// Levels returns the log levels for the countres.
func (h *PrometheusHook) Levels() []log.Level {
	return log.AllLevels
}

// Fire updates prometheus log metrics.
func (h *PrometheusHook) Fire(e *log.Entry) error {
	h.lcounter.WithLabelValues(e.Level.String()).Inc()
	h.mcounter.WithLabelValues(e.Level.String(), e.Message).Inc()
	return nil
}
