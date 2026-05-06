package gokue

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/dispatcher"
	jobpkg "github.com/zt4ff/gokue/job"
	"github.com/zt4ff/gokue/stats"
)

type Job = jobpkg.Job

type Option func(*config.Config)

type Queue struct {
	config     config.Config
	dispatcher *dispatcher.Dispatcher
	collector  *stats.Collector
	jobs       map[string]struct{}
	mu         sync.RWMutex
}

func WithConfig(cfg config.Config) Option {
	return func(target *config.Config) {
		*target = cfg
	}
}

func WithWorkerCount(workerCount int) Option {
	return func(target *config.Config) {
		target.WorkerCount = workerCount
	}
}

func WithQueueSize(queueSize int) Option {
	return func(target *config.Config) {
		target.QueueSize = queueSize
	}
}

func WithMaxRetries(maxRetries int) Option {
	return func(target *config.Config) {
		target.MaxRetries = maxRetries
	}
}

func WithJobTimeout(timeout time.Duration) Option {
	return func(target *config.Config) {
		target.JobTimeout = timeout
	}
}

func WithRetryDelay(delay time.Duration) Option {
	return func(target *config.Config) {
		target.RetryDelay = delay
	}
}

func WithShutdownTimeout(timeout time.Duration) Option {
	return func(target *config.Config) {
		target.ShutdownTimeout = timeout
	}
}

func NewQueue(options ...Option) (*Queue, error) {
	queueConfig := config.Default()
	for _, option := range options {
		if option != nil {
			option(&queueConfig)
		}
	}

	if err := queueConfig.Validate(); err != nil {
		return nil, err
	}

	collector := stats.NewCollector()
	queue := &Queue{
		config:    queueConfig,
		collector: collector,
		jobs:      make(map[string]struct{}),
	}
	queue.dispatcher = dispatcher.New(queueConfig, collector)

	return queue, nil
}

func (q *Queue) RegisterJob(name string) {
	if q == nil {
		return
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return
	}

	q.mu.Lock()
	q.jobs[name] = struct{}{}
	q.mu.Unlock()
}

func (q *Queue) Submit(ctx context.Context, name string, task Job) error {
	if q == nil {
		return errors.New("queue is nil")
	}
	if task == nil {
		return errors.New("job cannot be nil")
	}
	if ctx == nil {
		return errors.New("context cannot be nil")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = q.jobName(task)
	}

	q.mu.RLock()
	_, registered := q.jobs[name]
	registeredCount := len(q.jobs)
	q.mu.RUnlock()
	if registeredCount > 0 && !registered {
		return fmt.Errorf("job %q is not registered", name)
	}

	return q.dispatcher.Submit(ctx, dispatcher.Task{Name: name, Job: task})
}

func (q *Queue) TrySubmit(ctx context.Context, name string, task Job) error {
	if q == nil {
		return errors.New("queue is nil")
	}
	if task == nil {
		return errors.New("job cannot be nil")
	}
	if ctx == nil {
		return errors.New("context cannot be nil")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = q.jobName(task)
	}

	q.mu.RLock()
	_, registered := q.jobs[name]
	registeredCount := len(q.jobs)
	q.mu.RUnlock()
	if registeredCount > 0 && !registered {
		return fmt.Errorf("job %q is not registered", name)
	}

	return q.dispatcher.TrySubmit(ctx, dispatcher.Task{Name: name, Job: task})
}

func (q *Queue) Run(task Job) {
	if q == nil {
		return
	}
	_ = q.Submit(context.Background(), q.jobName(task), task)
}

func (q *Queue) Close(ctx context.Context) error {
	if q == nil {
		return errors.New("queue is nil")
	}
	if ctx == nil {
		return errors.New("context cannot be nil")
	}
	return q.dispatcher.Close(ctx)
}

func (q *Queue) Stats() stats.Snapshot {
	if q == nil || q.collector == nil {
		return stats.Snapshot{}
	}
	return q.collector.Snapshot()
}

func (q *Queue) jobName(task Job) string {
	if task == nil {
		return "anonymous-job"
	}

	typ := reflect.TypeOf(task)
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Name() != "" {
		return typ.Name()
	}

	return fmt.Sprintf("%T", task)
}
