package telemetry

import (
	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
)

type provider struct {
	tracer     observability.TraceCtx
	logger     observability.Logger
	counters   map[string]observability.Counter
	histograms map[string]observability.Histogram
}

// New assembles a Telemetry provider backed by the supplied tracer, logger, and metric instruments.
func New(
	tracer observability.TraceCtx,
	logger observability.Logger,
	counters map[string]observability.Counter,
	histograms map[string]observability.Histogram,
) observability.Telemetry {
	if tracer == nil {
		tracer = observability.NopTracer()
	}
	if logger == nil {
		logger = observability.NopLogger()
	}

	counterCopy := make(map[string]observability.Counter, len(counters))
	for k, v := range counters {
		if v != nil {
			counterCopy[k] = v
		}
	}

	histogramCopy := make(map[string]observability.Histogram, len(histograms))
	for k, v := range histograms {
		if v != nil {
			histogramCopy[k] = v
		}
	}

	return &provider{
		tracer:     tracer,
		logger:     logger,
		counters:   counterCopy,
		histograms: histogramCopy,
	}
}

func (p *provider) Tracer() observability.TraceCtx {
	return p.tracer
}

func (p *provider) Counter(name string) observability.Counter {
	if p == nil {
		return nil
	}
	return p.counters[name]
}

func (p *provider) Histogram(name string) observability.Histogram {
	if p == nil {
		return nil
	}
	return p.histograms[name]
}

func (p *provider) Logger() observability.Logger {
	return p.logger
}
