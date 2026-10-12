package job

import (
	"context"
	"testing"
)

type testJob struct{}

func (t *testJob) Process(context.Context) error { return nil }

type valueTestJob struct{}

func (t valueTestJob) Process(context.Context) error { return nil }

type namedTestJob struct{}

func (t *namedTestJob) Process(context.Context) error { return nil }

func (t *namedTestJob) Name() string { return "explicit-name" }

type emptyNamedTestJob struct{}

func (t *emptyNamedTestJob) Process(context.Context) error { return nil }

func (t *emptyNamedTestJob) Name() string { return "" }

type jobFunc func(context.Context) error

func (f jobFunc) Process(ctx context.Context) error { return f(ctx) }

func TestNameOf(t *testing.T) {
	testcases := map[string]struct {
		job    Job
		expect string
	}{
		"nil job": {
			job:    nil,
			expect: "anonymous",
		},
		"pointer to named struct": {
			job:    &testJob{},
			expect: "testJob",
		},
		"value of named struct": {
			job:    valueTestJob{},
			expect: "valueTestJob",
		},
		"named job uses Name method": {
			job:    &namedTestJob{},
			expect: "explicit-name",
		},
		"named job with empty Name falls back to type": {
			job:    &emptyNamedTestJob{},
			expect: "emptyNamedTestJob",
		},
		"anonymous struct": {
			job:    struct{ valueTestJob }{},
			expect: "anonymous",
		},
		"named function type": {
			job:    jobFunc(func(context.Context) error { return nil }),
			expect: "jobFunc",
		},
	}

	for name, testcase := range testcases {
		t.Run(name, func(t *testing.T) {
			if got := NameOf(testcase.job); got != testcase.expect {
				t.Errorf("expected %q, got %q", testcase.expect, got)
			}
		})
	}
}
