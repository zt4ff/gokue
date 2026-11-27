package gokue

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	jobpkg "github.com/zt4ff/gokue/v2/job"
)

// defaultDeadJobsLimit caps GET /dead when no limit is given.
const defaultDeadJobsLimit = 100

// DeadJob is how AdminHandler reports a failed job. It omits the payload,
// which may hold sensitive data.
type DeadJob struct {
	ID         jobpkg.ID   `json:"id"`
	Name       jobpkg.Name `json:"name"`
	Attempts   uint        `json:"attempts"`
	CreatedAt  time.Time   `json:"created_at"`
	StartedAt  *time.Time  `json:"started_at,omitempty"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
	LastError  string      `json:"last_error"`
}

// AdminHandler returns an http.Handler for inspecting the queue at runtime.
// Paths are relative to where it is mounted, so strip any prefix:
//
//	mux.Handle("/admin/queue/", http.StripPrefix("/admin/queue", q.AdminHandler()))
//
// Routes:
//
//	GET  /stats             Stats as JSON
//	GET  /dead?limit=N      failed jobs as JSON, oldest first (default limit 100, 0 for all)
//	POST /dead/{id}/replay  Replay the job; 204 on success
//
// The dead-letter routes need a queue created with WithStore and respond 501
// otherwise. Replay responds 404 for an unknown ID and 409 when the job is not
// failed or was changed concurrently. Errors are JSON objects with an "error"
// field.
//
// The handler has no authentication and replay changes state: serve it only
// on an internal listener or behind your own auth middleware.
func (q *Queue) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, q.Stats())
	})
	mux.HandleFunc("GET /dead", func(w http.ResponseWriter, r *http.Request) {
		limit := int64(defaultDeadJobsLimit)
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, errors.New("limit must be a non-negative integer"))
				return
			}
			limit = n
		}
		recs, err := q.DeadJobs(r.Context(), limit)
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		out := make([]DeadJob, 0, len(recs))
		for _, rec := range recs {
			out = append(out, DeadJob{
				ID:         rec.ID,
				Name:       rec.Name,
				Attempts:   rec.Attempts,
				CreatedAt:  rec.CreatedAt,
				StartedAt:  rec.StartedAt,
				FinishedAt: rec.FinishedAt,
				LastError:  rec.LastError,
			})
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /dead/{id}/replay", func(w http.ResponseWriter, r *http.Request) {
		if err := q.Replay(r.Context(), jobpkg.ID(r.PathValue("id"))); err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func adminStatus(err error) int {
	switch {
	case errors.Is(err, ErrNotPersistent):
		return http.StatusNotImplemented
	case errors.Is(err, backend.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrNotFailed), errors.Is(err, backend.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrNilQueue):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
