package logger

import (
	"fmt"
	"time"
)

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

// LogSubmitEnqueued logs a successful job submission.
func LogSubmitEnqueued(logger Logger, jobName string) {
	if logger == nil {
		return
	}
	logger.Debug(string(EventSubmitEnqueued),
		string(FieldJobName), jobName,
		string(FieldStatus), string(StatusEnqueued))
}

// LogSubmitRejected logs a rejected job submission.
func LogSubmitRejected(logger Logger, jobName string, reason Reason) {
	if logger == nil {
		return
	}
	logger.Warn(string(EventSubmitRejected),
		string(FieldJobName), jobName,
		string(FieldReason), string(reason))
}

// LogJobProcessing logs the start of job execution.
func LogJobProcessing(logger Logger, jobName string, attempt int) {
	if logger == nil {
		return
	}
	logger.Debug(string(EventJobProcessing),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt)
}

// LogJobRetry logs a job retry attempt.
func LogJobRetry(logger Logger, jobName string, attempt int, err error, delay time.Duration) {
	if logger == nil {
		return
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	logger.Info(string(EventJobRetry),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt,
		string(FieldError), errStr,
		string(FieldRetryDelayMs), delay.Milliseconds())
}

// LogJobSuccess logs successful job completion.
func LogJobSuccess(logger Logger, jobName string, duration time.Duration) {
	if logger == nil {
		return
	}
	logger.Info(string(EventJobCompleted),
		string(FieldJobName), jobName,
		string(FieldStatus), string(StatusSuccess),
		string(FieldDurationMs), duration.Milliseconds())
}

// LogJobFailure logs final job failure after all retries.
func LogJobFailure(logger Logger, jobName string, attempt int, err error, duration time.Duration) {
	if logger == nil {
		return
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	logger.Error(string(EventJobFailed),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt,
		string(FieldError), errStr,
		string(FieldDurationMs), duration.Milliseconds(),
		string(FieldStatus), string(StatusFinalFailure))
}

// LogJobAbandoned logs a job abandoned during dispatcher shutdown.
func LogJobAbandoned(logger Logger, jobName string, attempt int, reason Reason, duration time.Duration) {
	if logger == nil {
		return
	}
	logger.Warn(string(EventJobAbandoned),
		string(FieldJobName), jobName,
		string(FieldAttempt), attempt,
		string(FieldReason), string(reason),
		string(FieldDurationMs), duration.Milliseconds())
}

// LogCloseStart logs the start of dispatcher shutdown.
func LogCloseStart(logger Logger, mode string) {
	if logger == nil {
		return
	}
	logger.Info(string(EventCloseStarted), string(FieldMode), mode)
}

// LogCloseComplete logs the completion of dispatcher shutdown.
func LogCloseComplete(logger Logger, mode string, duration time.Duration, err error) {
	if logger == nil {
		return
	}
	fields := []interface{}{string(FieldMode), mode, string(FieldDurationMs), duration.Milliseconds()}
	if err != nil {
		fields = append(fields, string(FieldError), err.Error())
		logger.Error(string(EventCloseCompleted), fields...)
	} else {
		fields = append(fields, string(FieldStatus), string(StatusSuccess))
		logger.Info(string(EventCloseCompleted), fields...)
	}
}

// LogJobPanic logs a panic that occurred during job execution.
func LogJobPanic(logger Logger, jobName string, recovered interface{}) {
	if logger == nil {
		return
	}
	logger.Error(string(EventJobPanic),
		string(FieldJobName), jobName,
		string(FieldPanic), fmt.Sprintf("%v", recovered))
}

// LogWorkerPanic logs a panic in the dispatcher worker itself.
func LogWorkerPanic(logger Logger, recovered interface{}) {
	if logger == nil {
		return
	}
	logger.Error(string(EventWorkerPanic), string(FieldPanic), fmt.Sprintf("%v", recovered))
}
