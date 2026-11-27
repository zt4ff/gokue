package gokue

import (
	"encoding/json"
	"net/http"
)

// HealthStatus is the overall state reported by Queue.Health.
type HealthStatus string

const (
	// HealthOK means the queue accepts jobs without blocking.
	HealthOK HealthStatus = "ok"
	// HealthSaturated means the queue is full: Submit blocks and TrySubmit
	// returns ErrQueueFull until workers catch up. Persistent queues have no
	// capacity limit and are never saturated.
	HealthSaturated HealthStatus = "saturated"
	// HealthClosed means Close or CloseWithMode has been called and the queue
	// no longer accepts jobs.
	HealthClosed HealthStatus = "closed"
)

// Health describes whether a queue can take work, for readiness and liveness
// checks.
type Health struct {
	Status HealthStatus `json:"status"`
	// QueueDepth and QueueSize are the jobs waiting in the queue and its
	// capacity. QueueSize is 0 for persistent queues, which have no capacity.
	QueueDepth int `json:"queue_depth"`
	QueueSize  int `json:"queue_size"`
	// Running and IdleWorkers are as in stats.Snapshot.
	Running     int `json:"running"`
	IdleWorkers int `json:"idle_workers"`
}

// Health reports whether the queue is open and has room for more jobs. A nil
// queue reports HealthClosed.
func (q *Queue) Health() Health {
	if q == nil || q.dispatcher == nil {
		return Health{Status: HealthClosed}
	}
	s := q.Stats()
	h := Health{
		Status:      HealthOK,
		QueueDepth:  s.QueueDepth,
		Running:     s.Running,
		IdleWorkers: s.IdleWorkers,
	}
	if q.config.Store == nil {
		h.QueueSize = q.config.QueueSize
	}
	switch {
	case q.closed.Load():
		h.Status = HealthClosed
	case h.QueueSize > 0 && h.QueueDepth >= h.QueueSize:
		h.Status = HealthSaturated
	}
	return h
}

// HealthHandler returns an http.Handler that writes Health as JSON. It
// responds 200 OK when the status is HealthOK and 503 Service Unavailable when
// the queue is saturated or closed, so it can back a readiness probe:
//
//	mux.Handle("/healthz/queue", q.HealthHandler())
func (q *Queue) HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := q.Health()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if h.Status != HealthOK {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(h)
	})
}
