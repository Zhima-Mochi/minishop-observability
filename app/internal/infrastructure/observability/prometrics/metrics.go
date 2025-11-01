package prometrics

import (
	"sync"

	"github.com/Zhima-Mochi/minishop-observability/app/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
)

// Registry exposes the subset of Prometheus registry functionality needed by the application.
type Registry interface {
	Counter(name string, help string, labelKeys ...string) observability.Counter
	Histogram(name string, help string, buckets []float64, labelKeys ...string) observability.Histogram
}

type registry struct {
	counters   sync.Map // name -> *prometheus.CounterVec
	histograms sync.Map // name -> *prometheus.HistogramVec
	namespace  string
	subsystem  string
}

func New(namespace, subsystem string) Registry {
	return &registry{namespace: namespace, subsystem: subsystem}
}

type counter struct{ v *prometheus.CounterVec }

func (c *counter) Add(d float64, labels ...observability.Label) {
	c.v.With(labelMap(labels)).Add(d)
}

func (c *counter) Bind(labels ...observability.Label) observability.BoundCounter {
	return &boundCounter{v: c.v, labels: labelMap(labels)}
}

type boundCounter struct {
	v      *prometheus.CounterVec
	labels prometheus.Labels
}

func (c *boundCounter) Add(d float64) {
	if c == nil || c.v == nil {
		return
	}
	c.v.With(c.labels).Add(d)
}

type histogram struct{ v *prometheus.HistogramVec }

func (h *histogram) Observe(v float64, labels ...observability.Label) {
	h.v.With(labelMap(labels)).Observe(v)
}

func (h *histogram) Bind(labels ...observability.Label) observability.BoundHistogram {
	return &boundHistogram{v: h.v, labels: labelMap(labels)}
}

type boundHistogram struct {
	v      *prometheus.HistogramVec
	labels prometheus.Labels
}

func (h *boundHistogram) Observe(v float64) {
	if h == nil || h.v == nil {
		return
	}
	h.v.With(h.labels).Observe(v)
}

func labelMap(ls []observability.Label) prometheus.Labels {
	m := make(prometheus.Labels, len(ls))
	for _, l := range ls {
		m[l.Key] = l.Value
	}
	return m
}

func (r *registry) Counter(name string, help string, labelKeys ...string) observability.Counter {
	// ensure only registered once
	if v, ok := r.counters.Load(name); ok {
		return &counter{v: v.(*prometheus.CounterVec)}
	}
	cv := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: r.namespace, Subsystem: r.subsystem, Name: name, Help: help,
	}, labelKeys)
	prometheus.MustRegister(cv)
	r.counters.Store(name, cv)
	return &counter{v: cv}
}

func (r *registry) Histogram(name string, help string, buckets []float64, labelKeys ...string) observability.Histogram {
	if v, ok := r.histograms.Load(name); ok {
		return &histogram{v: v.(*prometheus.HistogramVec)}
	}
	hv := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: r.namespace, Subsystem: r.subsystem, Name: name, Help: help, Buckets: buckets,
	}, labelKeys)
	prometheus.MustRegister(hv)
	r.histograms.Store(name, hv)
	return &histogram{v: hv}
}
