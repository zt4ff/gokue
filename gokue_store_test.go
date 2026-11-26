package gokue

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	jobpkg "github.com/zt4ff/gokue/v2/job"
)

var persistedRuns atomic.Int64

type persistedJob struct{ N int }

func (persistedJob) Process(context.Context) error {
	persistedRuns.Add(1)
	return nil
}

type notRegisteredJob struct{ N int }

func (notRegisteredJob) Process(context.Context) error { return nil }

func succeededCount(t *testing.T, store backend.JobStore) int {
	t.Helper()
	status := backend.StatusSucceeded
	recs, err := store.List(context.Background(), backend.ListFilter{Status: &status})
	if err != nil {
		t.Fatal(err)
	}
	return len(recs)
}

func TestWithStoreRequiresStoreAndCodec(t *testing.T) {
	cases := map[string]Option{
		"nil store": WithStore(nil, backend.NewCodec()),
		"nil codec": WithStore(backend.NewMemoryStore(), nil),
		"both nil":  WithStore(nil, nil),
		"lease":     WithLease(0),
		"poll":      WithPollInterval(-time.Second),
	}
	for name, opt := range cases {
		opts := []Option{WithStore(backend.NewMemoryStore(), backend.NewCodec()), opt}
		if _, err := NewQueue(opts...); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: NewQueue error = %v, want ErrInvalidConfig", name, err)
		}
	}
}

// Jobs submitted to one queue and left in the store when it closes are run by
// the next queue opened on the same store.
func TestWithStoreJobsSurviveQueueRestart(t *testing.T) {
	persistedRuns.Store(0)
	store := backend.NewMemoryStore()
	codec := backend.NewCodec()
	if err := codec.Register("", persistedJob{}); err != nil {
		t.Fatal(err)
	}
	opts := []Option{WithStore(store, codec), WithWorkerCount(2), WithPollInterval(10 * time.Millisecond)}

	first, err := NewQueue(opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Submit(context.Background(), notRegisteredJob{N: 1}); !errors.Is(err, backend.ErrJobNotRegistered) {
		t.Fatalf("Submit unregistered = %v, want ErrJobNotRegistered", err)
	}
	// Stop the workers first, so the jobs below are only saved.
	if err := first.CloseWithMode(context.Background(), ImmediateMode); err != nil {
		t.Fatal(err)
	}
	name, payload, err := codec.Encode(persistedJob{N: 1})
	if err != nil {
		t.Fatal(err)
	}
	const jobs = 5
	for i := 0; i < jobs; i++ {
		rec := backend.JobRecord{ID: jobpkg.ID(fmt.Sprintf("saved-%d", i)), Name: name, Payload: payload, CreatedAt: time.Now()}
		if err := store.Save(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}

	second, err := NewQueue(opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Run(persistedJob{N: 2}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for succeededCount(t, store) != jobs+1 {
		if time.Now().After(deadline) {
			t.Fatalf("succeeded = %d, want %d", succeededCount(t, store), jobs+1)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := persistedRuns.Load(); got != jobs+1 {
		t.Fatalf("jobs ran %d times, want %d", got, jobs+1)
	}
	if s := second.Stats(); s.Processed != jobs+1 || s.Enqueued != 1 {
		t.Fatalf("stats = %+v", s)
	}
}

type alwaysFailsJob struct{ N int }

func (alwaysFailsJob) Process(context.Context) error { return errors.New("nope") }

func TestQueueDeadJobsAndReplay(t *testing.T) {
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

	var dead []backend.JobRecord
	deadline := time.Now().Add(5 * time.Second)
	for len(dead) == 0 && time.Now().Before(deadline) {
		if dead, err = q.DeadJobs(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(dead) != 1 || dead[0].LastError != "nope" {
		t.Fatalf("DeadJobs = %+v", dead)
	}
	if err := q.Replay(context.Background(), dead[0].ID); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	plain, err := NewQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close(context.Background()) }()
	if _, err := plain.DeadJobs(context.Background(), 0); !errors.Is(err, ErrNotPersistent) {
		t.Fatalf("DeadJobs on in-memory queue = %v, want ErrNotPersistent", err)
	}
	var nilQueue *Queue
	if err := nilQueue.Replay(context.Background(), "x"); !errors.Is(err, ErrNilQueue) {
		t.Fatalf("Replay on nil queue = %v, want ErrNilQueue", err)
	}
}
