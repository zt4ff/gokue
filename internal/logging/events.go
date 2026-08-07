package logging

// Event is the identifier of a structured log event emitted by gokue.
type Event string

const (
	// EventSubmitEnqueued is emitted when a job is accepted into the queue.
	EventSubmitEnqueued Event = "job_submitted"
	// EventSubmitRejected is emitted when a job submission is rejected.
	EventSubmitRejected Event = "job_submission_rejected"
	// EventJobProcessing is emitted when job execution starts.
	EventJobProcessing Event = "job_processing_started"
	// EventJobRetry is emitted when a job is scheduled for retry.
	EventJobRetry Event = "job_retry"
	// EventJobCompleted is emitted when a job finishes successfully.
	EventJobCompleted Event = "job_completed"
	// EventJobFailed is emitted when a job fails after all retries.
	EventJobFailed Event = "job_failed"
	// EventJobAbandoned is emitted when a job is abandoned mid-retry during shutdown.
	EventJobAbandoned Event = "job_abandoned"
	// EventJobPanic is emitted when a job panics during execution.
	EventJobPanic Event = "job_panic"
	// EventWorkerPanic is emitted when a dispatcher worker panics.
	EventWorkerPanic Event = "worker_panic"
	// EventCloseStarted is emitted when the dispatcher starts shutting down.
	EventCloseStarted Event = "dispatcher_close_started"
	// EventCloseCompleted is emitted when the dispatcher finishes shutting down.
	EventCloseCompleted Event = "dispatcher_close_completed"
)

// Field is the key of a structured log field.
type Field string

const (
	// FieldJobName is the name of the job.
	FieldJobName Field = "job_name"
	// FieldAttempt is the 1-based attempt number.
	FieldAttempt Field = "attempt"
	// FieldError is the error message.
	FieldError Field = "error"
	// FieldDurationMs is the elapsed duration in milliseconds.
	FieldDurationMs Field = "duration_ms"
	// FieldRetryDelayMs is the delay before the next retry in milliseconds.
	FieldRetryDelayMs Field = "retry_delay_ms"
	// FieldMode is the dispatcher shutdown mode.
	FieldMode Field = "mode"
	// FieldStatus is the outcome of an event.
	FieldStatus Field = "status"
	// FieldReason is why a submission was rejected or a job abandoned.
	FieldReason Field = "reason"
	// FieldPanic is the recovered panic value.
	FieldPanic Field = "panic"
)

// Status is the value of a status field.
type Status string

const (
	// StatusEnqueued indicates the job was accepted into the queue.
	StatusEnqueued Status = "enqueued"
	// StatusSuccess indicates successful completion.
	StatusSuccess Status = "success"
	// StatusFinalFailure indicates failure after all retries.
	StatusFinalFailure Status = "final_failure"
)

// Reason is the value of a reason field.
type Reason string

const (
	// ReasonDispatcherClosed indicates the dispatcher was already closed.
	ReasonDispatcherClosed Reason = "dispatcher_closed"
	// ReasonContextCancelled indicates the submit context was cancelled.
	ReasonContextCancelled Reason = "context_cancelled"
	// ReasonQueueFull indicates the queue was at capacity.
	ReasonQueueFull Reason = "queue_full"
	// ReasonDispatcherShutdown indicates the job was abandoned during shutdown.
	ReasonDispatcherShutdown Reason = "dispatcher_shutdown"
)
