// Package job defines the Job interface and related types for task execution in gokue.
package job

import "time"

// ID is a unique identifier for a job execution.
// ID is a unique identifier for a job execution.
type ID string

// Name is the name/label for a job.
type Name string

// JobDetails contains metadata and execution history for a job.
type JobDetails struct {
	// Job is the executable job implementation.
	Job Job
	// ID is the unique identifier for this job execution.
	ID ID
	// Name is the name of the job.
	Name Name
	// Attempts is the number of times this job has been attempted.
	Attempts int
	// CreatedAt is the timestamp when the job was created.
	CreatedAt time.Time
	// StartedAt is the timestamp when the job execution started.
	StartedAt time.Time
	// FinishedAt is the timestamp when the job execution finished.
	FinishedAt time.Time
}
