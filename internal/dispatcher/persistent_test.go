package dispatcher_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zt4ff/gokue/v2/backend"
	"github.com/zt4ff/gokue/v2/config"
	"github.com/zt4ff/gokue/v2/internal/dispatcher"
	"github.com/zt4ff/gokue/v2/job"
	"github.com/zt4ff/gokue/v2/stats"
)

// storedJob is a serializable job whose behaviour is looked up by Key, so a
// decoded copy behaves like the submitted one.
type storedJob struct{ Key string }

var storedJobs = struct {
	sync.Mutex
	behaviour map[string]func(context.Context) error
	runs      map[string]int
}{behaviour: map[string]func(context.Context) error{}, runs: map[string]int{}}

func (j storedJob) Process(ctx context.Context) error {
	storedJobs.Lock()
	storedJobs.runs[j.Key]++
	fn := storedJobs.behaviour[j.Key]
	storedJobs.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

func behave(key string, fn func(context.Context) error) storedJob {
	storedJobs.Lock()
	storedJobs.behaviour[key] = fn
	storedJobs.runs[key] = 0
	storedJobs.Unlock()
	return storedJob{Key: key}
}

func runsOf(key string) int {
	storedJobs.Lock()
	defer storedJobs.Unlock()
	return storedJobs.runs[key]
}

type unregisteredJob struct{ N int }

func (unregisteredJob) Process(context.Context) error { return nil }

func storeConfig(t *testing.T, store backend.JobStore) config.Config {
	t.Helper()
	codec := backend.NewCodec()
	if err := codec.Register("", storedJob{}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MemoryType = config.Persistent
	cfg.Store = store
	cfg.Codec = codec
	cfg.WorkerCount = 2
	cfg.MaxRetries = 0
	cfg.RetryDelay = 0
	cfg.Lease = time.Minute
	cfg.PollInterval = 10 * time.Millisecond
	return cfg
}

func statusCount(t *testing.T, store backend.JobStore, status backend.JobStatus) int {
	t.Helper()
	recs, err := store.List(context.Background(), backend.ListFilter{Status: &status})
	if err != nil {
		t.Fatal(err)
	}
	return len(recs)
}

func onlyRecord(t *testing.T, store backend.JobStore) backend.JobRecord {
	t.Helper()
	recs, err := store.List(context.Background(), backend.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	return recs[0]
}

func TestStoreSubmitRunsAndRecordsSuccess(t *testing.T) {
	store := backend.NewMemoryStore()
	d := dispatcher.NewDispatcher(storeConfig(t, store), stats.NewCollector())

	if err := d.Submit(context.Background(), dispatcher.Task{Job: behave(t.Name(), nil)}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == 1 })

	rec := onlyRecord(t, store)
	if rec.FinishedAt == nil || rec.Attempts != 1 || rec.Name != "storedJob" {
		t.Fatalf("record = %+v", rec)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := d.Stats(); s.Enqueued != 1 || s.Processed != 1 || s.QueueDepth != 0 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestStoreSubmitRejectsUnregisteredJob(t *testing.T) {
	store := backend.NewMemoryStore()
	d := dispatcher.NewDispatcher(storeConfig(t, store), stats.NewCollector())
	defer d.Close(context.Background())

	err := d.Submit(context.Background(), dispatcher.Task{Job: unregisteredJob{N: 1}})
	if !errors.Is(err, backend.ErrJobNotRegistered) {
		t.Fatalf("Submit error = %v, want ErrJobNotRegistered", err)
	}
	if n := statusCount(t, store, backend.StatusPending); n != 0 {
		t.Fatalf("saved %d records for a rejected job", n)
	}
}

func TestStoreRecordsFailureAfterRetries(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.MaxRetries = 2
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer d.Close(context.Background())

	boom := errors.New("boom")
	j := behave(t.Name(), func(context.Context) error { return boom })
	if err := d.TrySubmit(context.Background(), dispatcher.Task{Job: j}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusFailed) == 1 })
	if got := runsOf(t.Name()); got != 3 {
		t.Fatalf("ran %d times, want 3", got)
	}
	if rec := onlyRecord(t, store); rec.FinishedAt == nil || rec.Attempts != 3 {
		t.Fatalf("record = %+v", rec)
	}
}

func TestStorePerSubmitOverrideApplies(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.MaxRetries = 5
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer d.Close(context.Background())

	zero := 0
	j := behave(t.Name(), func(context.Context) error { return errors.New("boom") })
	if err := d.Submit(context.Background(), dispatcher.Task{Job: j, MaxRetries: &zero}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusFailed) == 1 })
	if got := runsOf(t.Name()); got != 1 {
		t.Fatalf("ran %d times, want 1", got)
	}
}

// A new dispatcher picks up jobs a previous process saved but never ran.
func TestStoreRunsPendingJobsFromEarlierProcess(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)

	first := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	if err := first.CloseWithMode(context.Background(), dispatcher.ImmediateMode); err != nil {
		t.Fatal(err)
	}
	name, payload, err := cfg.Codec.Encode(behave(t.Name(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), backend.JobRecord{ID: "left-behind", Name: name, Payload: payload, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	second := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer second.Close(context.Background())
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == 1 })
	if got := runsOf(t.Name()); got != 1 {
		t.Fatalf("ran %d times, want 1", got)
	}
}

// A job claimed by a process that died is run again once its lease expires.
func TestStoreRecoversJobOfCrashedWorker(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.Lease = 60 * time.Millisecond

	name, payload, err := cfg.Codec.Encode(behave(t.Name(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), backend.JobRecord{ID: "crashed", Name: name, Payload: payload, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background()); err != nil { // the "crashed" worker
		t.Fatal(err)
	}

	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer d.Close(context.Background())
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == 1 })

	rec, err := store.Get(context.Background(), job.ID("crashed"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Attempts != 2 || runsOf(t.Name()) != 1 {
		t.Fatalf("attempts = %d, runs = %d; want 2, 1", rec.Attempts, runsOf(t.Name()))
	}
}

// A job that runs longer than the lease keeps it through heartbeats.
func TestStoreHeartbeatKeepsLongJob(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.Lease = 200 * time.Millisecond
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer d.Close(context.Background())

	j := behave(t.Name(), func(context.Context) error {
		time.Sleep(3 * cfg.Lease)
		return nil
	})
	if err := d.Submit(context.Background(), dispatcher.Task{Job: j}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == 1 })
	if rec := onlyRecord(t, store); rec.Attempts != 1 || runsOf(t.Name()) != 1 {
		t.Fatalf("attempts = %d, runs = %d; want 1, 1", rec.Attempts, runsOf(t.Name()))
	}
}

func TestStoreMarksUndecodableRecordFailed(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	if err := store.Save(context.Background(), backend.JobRecord{ID: "unknown", Name: "gone", Payload: []byte(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer d.Close(context.Background())

	mustWait(t, func() bool { return statusCount(t, store, backend.StatusFailed) == 1 })
	if s := d.Stats(); s.Failed != 1 {
		t.Fatalf("stats = %+v", s)
	}
}

// Drain finishes claimed jobs and leaves unclaimed ones in the store.
func TestStoreDrainLeavesUnclaimedJobs(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.WorkerCount = 1
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())

	release := make(chan struct{})
	started := make(chan struct{}, 3)
	j := behave(t.Name(), func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	})
	for i := 0; i < 3; i++ {
		if err := d.Submit(context.Background(), dispatcher.Task{Job: j}); err != nil {
			t.Fatal(err)
		}
	}
	<-started

	closed := make(chan error, 1)
	go func() { closed <- d.Close(context.Background()) }()
	mustWait(t, func() bool {
		return errors.Is(d.Submit(context.Background(), dispatcher.Task{Job: j}), dispatcher.ErrClosed)
	})
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}

	// The probe above may have saved extra jobs before Close took effect.
	ok, pending := statusCount(t, store, backend.StatusSucceeded), statusCount(t, store, backend.StatusPending)
	if ok != 1 || pending < 2 || runsOf(t.Name()) != 1 {
		t.Fatalf("succeeded = %d, pending = %d, runs = %d; want 1, >=2, 1", ok, pending, runsOf(t.Name()))
	}
}

// A retry abandoned by shutdown goes back to pending instead of being lost.
func TestStoreAbandonedRetryReturnsToPending(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.MaxRetries = 3
	cfg.RetryDelay = time.Hour
	cfg.BackoffStrategy = config.Constant
	cfg.MaxRetryDelay = 0
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())

	j := behave(t.Name(), func(context.Context) error { return errors.New("boom") })
	if err := d.Submit(context.Background(), dispatcher.Task{Job: j}); err != nil {
		t.Fatal(err)
	}
	mustWait(t, func() bool { return d.Stats().Retried == 1 })
	if err := d.CloseWithMode(context.Background(), dispatcher.ImmediateMode); err != nil {
		t.Fatal(err)
	}

	mustWait(t, func() bool { return statusCount(t, store, backend.StatusPending) == 1 })
	if rec := onlyRecord(t, store); rec.StartedAt != nil || rec.FinishedAt != nil {
		t.Fatalf("record = %+v", rec)
	}
}

// Two dispatchers sharing a store run each job exactly once.
func TestStoreSharedByTwoDispatchers(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	a := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	b := dispatcher.NewDispatcher(cfg, stats.NewCollector())

	const jobs = 60
	j := behave(t.Name(), nil)
	for i := 0; i < jobs; i++ {
		if err := a.Submit(context.Background(), dispatcher.Task{Job: j}); err != nil {
			t.Fatal(err)
		}
	}
	mustWait(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == jobs })
	if got := runsOf(t.Name()); got != jobs {
		t.Fatalf("ran %d times, want %d", got, jobs)
	}
	if done := a.Stats().Processed + b.Stats().Processed; done != jobs {
		t.Fatalf("processed %d, want %d", done, jobs)
	}
	for _, d := range []*dispatcher.Dispatcher{a, b} {
		if err := d.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	if !waitFor(t, 5*time.Second, cond) {
		t.Fatal("condition not met before timeout")
	}
}

// A job that exhausts its retries is listed by DeadJobs with its last error,
// and Replay runs it again with fresh attempts.
func TestStoreDeadJobsAndReplay(t *testing.T) {
	store := backend.NewMemoryStore()
	cfg := storeConfig(t, store)
	cfg.MaxRetries = 1
	d := dispatcher.NewDispatcher(cfg, stats.NewCollector())
	defer func() { _ = d.Close(context.Background()) }()

	var fail sync.Mutex
	failing := true
	j := behave("dead", func(context.Context) error {
		fail.Lock()
		defer fail.Unlock()
		if failing {
			return errors.New("boom")
		}
		return nil
	})
	if err := d.Submit(context.Background(), dispatcher.Task{Name: "dead", Job: j}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return statusCount(t, store, backend.StatusFailed) == 1 })

	dead, err := d.DeadJobs(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0].LastError != "boom" || dead[0].FinishedAt == nil {
		t.Fatalf("DeadJobs = %+v, want one record with LastError boom", dead)
	}
	if runsOf("dead") != 2 {
		t.Fatalf("runs = %d, want 2", runsOf("dead"))
	}

	fail.Lock()
	failing = false
	fail.Unlock()
	if err := d.Replay(context.Background(), dead[0].ID); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	eventually(t, func() bool { return statusCount(t, store, backend.StatusSucceeded) == 1 })

	rec := onlyRecord(t, store)
	if rec.LastError != "" || rec.Attempts != 1 {
		t.Fatalf("replayed record = %+v, want no LastError and attempts 1", rec)
	}
	if err := d.Replay(context.Background(), rec.ID); !errors.Is(err, dispatcher.ErrNotFailed) {
		t.Fatalf("Replay succeeded record = %v, want ErrNotFailed", err)
	}
	if err := d.Replay(context.Background(), "missing"); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("Replay missing = %v, want ErrNotFound", err)
	}
	if dead, err := d.DeadJobs(context.Background(), 0); err != nil || len(dead) != 0 {
		t.Fatalf("DeadJobs after replay = %v, %v; want none", dead, err)
	}
}

func TestStoreDeadJobsLimitAndDecodeError(t *testing.T) {
	store := backend.NewMemoryStore()
	for _, id := range []job.ID{"a", "b", "c"} {
		if err := store.Save(context.Background(), backend.JobRecord{ID: id, Name: "unknown", Payload: []byte("{}"), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	d := dispatcher.NewDispatcher(storeConfig(t, store), stats.NewCollector())
	defer func() { _ = d.Close(context.Background()) }()
	eventually(t, func() bool { return statusCount(t, store, backend.StatusFailed) == 3 })

	dead, err := d.DeadJobs(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 2 || dead[0].LastError == "" {
		t.Fatalf("DeadJobs(2) = %+v, want 2 records with a decode error", dead)
	}
}

func TestDeadJobsRequireStore(t *testing.T) {
	d := dispatcher.NewDispatcher(config.Default(), stats.NewCollector())
	defer func() { _ = d.Close(context.Background()) }()
	if _, err := d.DeadJobs(context.Background(), 0); !errors.Is(err, dispatcher.ErrNotPersistent) {
		t.Fatalf("DeadJobs = %v, want ErrNotPersistent", err)
	}
	if err := d.Replay(context.Background(), "x"); !errors.Is(err, dispatcher.ErrNotPersistent) {
		t.Fatalf("Replay = %v, want ErrNotPersistent", err)
	}
}
