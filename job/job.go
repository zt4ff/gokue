package job

import "context"

// return error for job failure
type Job interface {
	Process(context.Context) error
}
