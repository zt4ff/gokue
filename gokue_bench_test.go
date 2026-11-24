package gokue

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
)

// noopJob does no work, so benchmarks measure queue overhead only.
type noopJob struct{}

func (noopJob) Process(context.Context) error { return nil }

// doneJob signals completion through wg.
type doneJob struct {
	wg *sync.WaitGroup
}

func (j *doneJob) Process(context.Context) error {
	j.wg.Done()
	return nil
}

// latencyJob records the time from submission to completion.
type latencyJob struct {
	start   time.Time
	latency time.Duration
	wg      *sync.WaitGroup
}

func (j *latencyJob) Process(context.Context) error {
	j.latency = time.Since(j.start)
	j.wg.Done()
	return nil
}

// flakyJob fails its first attempt and succeeds on the retry.
type flakyJob struct {
	attempts int
	wg       *sync.WaitGroup
}

var errBenchFlaky = errors.New("flaky")

func (j *flakyJob) Process(context.Context) error {
	j.attempts++
	if j.attempts == 1 {
		return errBenchFlaky
	}
	j.wg.Done()
	return nil
}

func newBenchQueue(b *testing.B, opts ...Option) *Queue {
	b.Helper()
	q, err := NewQueue(opts...)
	if err != nil {
		b.Fatal(err)
	}
	return q
}

func closeBenchQueue(b *testing.B, q *Queue) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := q.Close(ctx); err != nil {
		b.Fatal(err)
	}
}

func reportJobsPerSec(b *testing.B) {
	if s := b.Elapsed().Seconds(); s > 0 {
		b.ReportMetric(float64(b.N)/s, "jobs/s")
	}
}

func reportPercentiles(b *testing.B, latencies []time.Duration) {
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) float64 {
		return float64(latencies[int(p*float64(len(latencies)-1))].Nanoseconds())
	}
	b.ReportMetric(pct(0.50), "p50-ns")
	b.ReportMetric(pct(0.99), "p99-ns")
}

// BenchmarkSubmit measures the cost of Submit from one goroutine while the
// default worker pool drains the queue.
func BenchmarkSubmit(b *testing.B) {
	q := newBenchQueue(b)
	defer closeBenchQueue(b, q)
	ctx := context.Background()
	job := noopJob{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := q.Submit(ctx, job); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	reportJobsPerSec(b)
}

// BenchmarkSubmitParallel measures Submit from GOMAXPROCS goroutines at once.
func BenchmarkSubmitParallel(b *testing.B) {
	q := newBenchQueue(b)
	defer closeBenchQueue(b, q)
	ctx := context.Background()
	job := noopJob{}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := q.Submit(ctx, job); err != nil {
				b.Error(err)
				return
			}
		}
	})
	b.StopTimer()
	reportJobsPerSec(b)
}

// BenchmarkTrySubmit measures the non-blocking path. When the queue is full
// the submission is retried, so every op is one enqueued job.
func BenchmarkTrySubmit(b *testing.B) {
	q := newBenchQueue(b)
	defer closeBenchQueue(b, q)
	ctx := context.Background()
	job := noopJob{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for {
			err := q.TrySubmit(ctx, job)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrQueueFull) {
				b.Fatal(err)
			}
			runtime.Gosched()
		}
	}
	b.StopTimer()
	reportJobsPerSec(b)
}

// BenchmarkThroughput submits b.N jobs and waits until all of them complete,
// for several worker counts. It reports enqueue-to-complete latency under load.
func BenchmarkThroughput(b *testing.B) {
	for _, workers := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			q := newBenchQueue(b, WithWorkerCount(workers))
			defer closeBenchQueue(b, q)
			ctx := context.Background()

			var wg sync.WaitGroup
			jobs := make([]latencyJob, b.N)
			for i := range jobs {
				jobs[i].wg = &wg
			}
			wg.Add(b.N)

			b.ReportAllocs()
			b.ResetTimer()
			for i := range jobs {
				jobs[i].start = time.Now()
				if err := q.Submit(ctx, &jobs[i]); err != nil {
					b.Fatal(err)
				}
			}
			wg.Wait()
			b.StopTimer()

			latencies := make([]time.Duration, len(jobs))
			for i := range jobs {
				latencies[i] = jobs[i].latency
			}
			reportJobsPerSec(b)
			reportPercentiles(b, latencies)
		})
	}
}

// BenchmarkEndToEndLatency submits one job at a time to an idle queue and
// waits for it to complete, so each op is one full round trip.
func BenchmarkEndToEndLatency(b *testing.B) {
	q := newBenchQueue(b)
	defer closeBenchQueue(b, q)
	ctx := context.Background()

	var wg sync.WaitGroup
	job := &doneJob{wg: &wg}
	latencies := make([]time.Duration, b.N)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		wg.Add(1)
		if err := q.Submit(ctx, job); err != nil {
			b.Fatal(err)
		}
		wg.Wait()
		latencies[i] = time.Since(start)
	}
	b.StopTimer()
	reportPercentiles(b, latencies)
}

// BenchmarkRetry runs jobs that fail once and succeed on an immediate retry,
// measuring the overhead of the retry path.
func BenchmarkRetry(b *testing.B) {
	q := newBenchQueue(b, WithMaxRetries(1), WithRetryDelay(0))
	defer closeBenchQueue(b, q)
	ctx := context.Background()

	var wg sync.WaitGroup
	jobs := make([]flakyJob, b.N)
	for i := range jobs {
		jobs[i].wg = &wg
	}
	wg.Add(b.N)

	b.ReportAllocs()
	b.ResetTimer()
	for i := range jobs {
		if err := q.Submit(ctx, &jobs[i]); err != nil {
			b.Fatal(err)
		}
	}
	wg.Wait()
	b.StopTimer()
	reportJobsPerSec(b)

	if got := q.Stats().Retried; got < uint64(b.N) {
		b.Fatalf("retried = %d, want >= %d", got, b.N)
	}
}
