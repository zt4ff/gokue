// Package job defines the Job interface and related types for task execution in gokue.
package job

import (
	"context"
	"reflect"
)

// Job is the interface for tasks that can be executed by the dispatcher.
// Implementations should return an error if the job fails, or nil if it succeeds.
type Job interface {
	// Process executes the job with the given context.
	// The context can be used for cancellation and timeouts.
	// Returns an error if the job fails, nil if it succeeds.
	Process(context.Context) error
}

// NamedJob is a Job that provides an explicit name.
// If a job implements NamedJob, NameOf returns the value of its Name method
// instead of the job's type name.
type NamedJob interface {
	Job
	// Name returns the explicit name for the job.
	Name() string
}

// NameOf returns a stable identifier for the given job.
// It returns the job's Name() when the job implements NamedJob, otherwise
// it derives the name from the job's type. Nil jobs and anonymous types
// resolve to "anonymous".
func NameOf(j Job) string {
	if j == nil {
		return "anonymous"
	}

	if named, ok := j.(NamedJob); ok {
		if name := named.Name(); name != "" {
			return name
		}
	}

	typ := reflect.TypeOf(j)
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Name() != "" {
		return typ.Name()
	}

	return "anonymous"
}
