package job

import "time"

type ID string

type Name string

type JobDetails struct {
	Job        Job
	ID         ID
	Name       Name
	Attempts   int
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}
