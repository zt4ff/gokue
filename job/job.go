// Package job defines the Job interface and related types for task execution in gokue.
package job

import "context"

// Job is the interface for tasks that can be executed by the dispatcher.
// Implementations should return an error if the job fails, or nil if it succeeds.
type Job interface {
	// Process executes the job with the given context.
	// The context can be used for cancellation and timeouts.
	// Returns an error if the job fails, nil if it succeeds.
	Process(context.Context) error
}
