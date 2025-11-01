package observability

import (
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
)

type provider struct {
	tracer  observability.Tracer
	logger  observability.Logger
	metrics observability.Metrics
}

type registeredMetrics struct {
	counters   map[observability.MetricKey]observability.Counter
	histograms map[observability.MetricKey]observability.Histogram
}

func (m *registeredMetrics) Counter(name observability.MetricKey) observability.Counter {
	if m == nil || m.counters == nil {
		return observability.NopCounter()
	}
	if c, ok := m.counters[name]; ok && c != nil {
		return c
	}
	return observability.NopCounter()
}

func (m *registeredMetrics) Histogram(name observability.MetricKey) observability.Histogram {
	if m == nil || m.histograms == nil {
		return observability.NopHistogram()
	}
	if h, ok := m.histograms[name]; ok && h != nil {
		return h
	}
	return observability.NopHistogram()
}

// New assembles a Telemetry provider backed by the supplied tracer, logger, and metric instruments.
func New(
	tracer observability.Tracer,
	logger observability.Logger,
	counters map[observability.MetricKey]observability.Counter,
	histograms map[observability.MetricKey]observability.Histogram,
) observability.Observability {
	if tracer == nil {
		tracer = observability.NopTracer()
	}
	if logger == nil {
		logger = observability.NopLogger()
	}

	var metrics observability.Metrics = observability.NopMetrics()
	if len(counters) > 0 || len(histograms) > 0 {
		m := &registeredMetrics{
			counters:   make(map[observability.MetricKey]observability.Counter, len(counters)),
			histograms: make(map[observability.MetricKey]observability.Histogram, len(histograms)),
		}
		for k, v := range counters {
			if v == nil {
				continue
			}
			m.counters[k] = v
		}
		for k, v := range histograms {
			if v == nil {
				continue
			}
			m.histograms[k] = v
		}
		metrics = m
	}

	return &provider{
		tracer:  tracer,
		logger:  logger,
		metrics: metrics,
	}
}

func (p *provider) Tracer() observability.Tracer {
	return p.tracer
}

func (p *provider) Logger() observability.Logger {
	return p.logger
}

func (p *provider) Metrics() observability.Metrics {
	if p.metrics == nil {
		return observability.NopMetrics()
	}
	return p.metrics
}
