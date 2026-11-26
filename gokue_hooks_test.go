package gokue_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zt4ff/gokue/v2"
)

type hookRecorder struct {
	mu                        sync.Mutex
	success, failure, retries []gokue.JobEvent
}

func (r *hookRecorder) options() []gokue.Option {
	rec := func(dst *[]gokue.JobEvent) func(gokue.JobEvent) {
		return func(e gokue.JobEvent) {
			r.mu.Lock()
			*dst = append(*dst, e)
			r.mu.Unlock()
		}
	}
	return []gokue.Option{
		gokue.WithOnSuccess(rec(&r.success)),
		gokue.WithOnFailure(rec(&r.failure)),
		gokue.WithOnRetry(rec(&r.retries)),
	}
}

type namedFuncJob struct {
	name string
	fn   func(context.Context) error
}

func (j namedFuncJob) Name() string                      { return j.name }
func (j namedFuncJob) Process(ctx context.Context) error { return j.fn(ctx) }

func TestLifecycleHooksFireOncePerOutcome(t *testing.T) {
	r := &hookRecorder{}
	opts := append(r.options(), gokue.WithWorkerCount(1), gokue.WithMaxRetries(2), gokue.WithRetryDelay(0))
	q, err := gokue.NewQueue(opts...)
	if err != nil {
		t.Fatal(err)
	}
	errBoom := errors.New("boom")
	calls := 0
	jobs := []gokue.Job{
		namedFuncJob{"ok", func(context.Context) error { return nil }},
		namedFuncJob{"fail", func(context.Context) error { return errBoom }},
		namedFuncJob{"flaky", func(context.Context) error {
			calls++
			if calls < 2 {
				return errBoom
			}
			return nil
		}},
		namedFuncJob{"permanent", func(context.Context) error { return errBoom }},
	}
	for i, j := range jobs {
		var sopts []gokue.SubmitOption
		if i == 3 {
			sopts = append(sopts, gokue.RetryIf(func(error) bool { return false }))
		}
		if err := q.Submit(context.Background(), j, sopts...); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	names := func(es []gokue.JobEvent) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.Name)
		}
		return out
	}
	if got := names(r.success); len(got) != 2 || got[0] != "ok" || got[1] != "flaky" {
		t.Errorf("OnSuccess: %v", got)
	}
	if got := names(r.failure); len(got) != 2 || got[0] != "fail" || got[1] != "permanent" {
		t.Errorf("OnFailure: %v", got)
	}
	// "fail" retries twice, "flaky" once, "permanent" never.
	if got := names(r.retries); len(got) != 3 {
		t.Errorf("OnRetry: %v", got)
	}
	for _, e := range r.failure {
		if !errors.Is(e.Err, errBoom) {
			t.Errorf("failure event missing error: %+v", e)
		}
	}
	if r.failure[0].Attempt != 3 || r.failure[1].Attempt != 1 {
		t.Errorf("unexpected failure attempts: %+v", r.failure)
	}
	if r.success[1].Attempt != 2 {
		t.Errorf("flaky success attempt = %d, want 2", r.success[1].Attempt)
	}
}

func TestPanickingHookIsRecovered(t *testing.T) {
	q, err := gokue.NewQueue(
		gokue.WithWorkerCount(1),
		gokue.WithOnSuccess(func(gokue.JobEvent) { panic("hook boom") }),
	)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := q.Submit(context.Background(), funcJob(func(context.Context) error { return nil })); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := q.Stats(); s.Processed != 3 || s.Failed != 0 {
		t.Fatalf("hook panic changed outcomes: %+v", s)
	}
}
