package gokue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
)

type blockingJob struct{ release chan struct{} }

func (j blockingJob) Process(context.Context) error {
	<-j.release
	return nil
}

func getHealth(t *testing.T, h http.Handler) (int, Health) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body Health
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	return rec.Code, body
}

func TestHealthOKSaturatedClosed(t *testing.T) {
	q, err := NewQueue(WithWorkerCount(1), WithQueueSize(1))
	if err != nil {
		t.Fatal(err)
	}
	handler := q.HealthHandler()

	if code, h := getHealth(t, handler); code != http.StatusOK || h.Status != HealthOK || h.QueueSize != 1 || h.IdleWorkers != 1 {
		t.Fatalf("fresh queue: %d %+v", code, h)
	}

	release := make(chan struct{})
	job := blockingJob{release: release}
	if err := q.Submit(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for q.Stats().Running != 1 {
		if time.Now().After(deadline) {
			t.Fatal("worker did not pick up the job")
		}
		time.Sleep(time.Millisecond)
	}
	if err := q.Submit(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if code, h := getHealth(t, handler); code != http.StatusServiceUnavailable || h.Status != HealthSaturated || h.QueueDepth != 1 {
		t.Fatalf("full queue: %d %+v", code, h)
	}

	close(release)
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, h := getHealth(t, handler); code != http.StatusServiceUnavailable || h.Status != HealthClosed {
		t.Fatalf("closed queue: %d %+v", code, h)
	}
}

func TestHealthPersistentQueueHasNoCapacity(t *testing.T) {
	codec := backend.NewCodec()
	q, err := NewQueue(WithStore(backend.NewMemoryStore(), codec))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close(context.Background()) }()
	if h := q.Health(); h.Status != HealthOK || h.QueueSize != 0 {
		t.Fatalf("persistent queue: %+v", h)
	}
}

func TestHealthNilQueueAndHead(t *testing.T) {
	var q *Queue
	if h := q.Health(); h.Status != HealthClosed {
		t.Fatalf("nil queue: %+v", h)
	}
	rec := httptest.NewRecorder()
	q.HealthHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Body.Len() != 0 {
		t.Fatalf("HEAD on nil queue: %d %q", rec.Code, rec.Body.String())
	}
}
