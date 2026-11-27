package gokue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
)

func adminDo(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestAdminHandlerDeadJobsAndReplay(t *testing.T) {
	store := backend.NewMemoryStore()
	codec := backend.NewCodec()
	if err := codec.Register("", alwaysFailsJob{}); err != nil {
		t.Fatal(err)
	}
	q, err := NewQueue(WithStore(store, codec), WithMaxRetries(0), WithPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close(context.Background()) }()
	if err := q.Submit(context.Background(), alwaysFailsJob{}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", q.AdminHandler()))

	var dead []DeadJob
	deadline := time.Now().Add(5 * time.Second)
	for len(dead) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("job never reached the dead-letter queue")
		}
		rec := adminDo(t, mux, http.MethodGet, "/admin/dead?limit=10")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /dead = %d %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "Payload") || strings.Contains(rec.Body.String(), "payload") {
			t.Fatalf("GET /dead leaks the payload: %s", rec.Body)
		}
		if err := json.NewDecoder(rec.Body).Decode(&dead); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if dead[0].LastError != "nope" || dead[0].Attempts != 1 || dead[0].ID == "" {
		t.Fatalf("dead job = %+v", dead[0])
	}

	if rec := adminDo(t, mux, http.MethodPost, "/admin/dead/"+string(dead[0].ID)+"/replay"); rec.Code != http.StatusNoContent {
		t.Fatalf("replay = %d %s", rec.Code, rec.Body)
	}
	if rec := adminDo(t, mux, http.MethodPost, "/admin/dead/missing/replay"); rec.Code != http.StatusNotFound {
		t.Fatalf("replay missing = %d", rec.Code)
	}
	if rec := adminDo(t, mux, http.MethodGet, "/admin/dead?limit=x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit = %d", rec.Code)
	}
	if rec := adminDo(t, mux, http.MethodGet, "/admin/dead/x/replay"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET replay = %d, want 405", rec.Code)
	}

	rec := adminDo(t, mux, http.MethodGet, "/admin/stats")
	var s struct{ Enqueued, Failed uint64 }
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil || rec.Code != http.StatusOK || s.Enqueued != 1 || s.Failed == 0 {
		t.Fatalf("GET /stats = %d %+v %v", rec.Code, s, err)
	}
}

func TestAdminHandlerReplayNotFailedAndInMemory(t *testing.T) {
	store := backend.NewMemoryStore()
	if err := store.Save(context.Background(), backend.JobRecord{ID: "p", Name: "x", Status: backend.StatusSucceeded, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	q, err := NewQueue(WithStore(store, backend.NewCodec()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close(context.Background()) }()
	if rec := adminDo(t, q.AdminHandler(), http.MethodPost, "/dead/p/replay"); rec.Code != http.StatusConflict {
		t.Fatalf("replay succeeded job = %d", rec.Code)
	}

	plain, err := NewQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close(context.Background()) }()
	rec := adminDo(t, plain.AdminHandler(), http.MethodGet, "/dead")
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || rec.Code != http.StatusNotImplemented || body["error"] == "" {
		t.Fatalf("in-memory /dead = %d %v %v", rec.Code, body, err)
	}
}
