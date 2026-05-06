package goku

import (
	"context"
	"goku/config"
)

// multiple queues
// queues can have multiple job kinds
// append jobs

type Queue struct {
	config config.Config
	job    Job
}

// TODO - pass required config in creation via arguments
func NewQueue() (*Queue, error) {
	// TODO set configs
	// - memory is set from the config

	return &Queue{}, nil
}

// TODO - set config with functional options
func (q *Queue) SetConfig() {
	// TODO
}

// process interface
type Job interface {
	// error signifies a failing job
	Process(context.Context) error
}

// job registration
func (q *Queue) RegisterJob(name string) {
	// save job information with the stat collector
}

func (q *Queue) Run(job Job) {
	ctx := context.Background()
	go q.job.Process(ctx)
}
