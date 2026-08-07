package dispatcher

import (
	"testing"
	"time"

	"github.com/zt4ff/gokue/config"
)

func TestRetryDelayConstant(t *testing.T) {
	base := 2 * time.Second
	if got := retryDelay(base, 0, 0, config.Constant); got != base {
		t.Errorf("expected %v, got %v", base, got)
	}
	if got := retryDelay(base, 0, 5, config.Constant); got != base {
		t.Errorf("expected %v, got %v", base, got)
	}
}

func TestRetryDelayLinear(t *testing.T) {
	base := 2 * time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 2 * time.Second},
		{1, 4 * time.Second},
		{2, 6 * time.Second},
	}
	for _, tc := range cases {
		if got := retryDelay(base, 0, tc.attempt, config.Linear); got != tc.want {
			t.Errorf("attempt %d: expected %v, got %v", tc.attempt, tc.want, got)
		}
	}
}

func TestRetryDelayExponential(t *testing.T) {
	base := 1 * time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
	}
	for _, tc := range cases {
		if got := retryDelay(base, 0, tc.attempt, config.Exponential); got != tc.want {
			t.Errorf("attempt %d: expected %v, got %v", tc.attempt, tc.want, got)
		}
	}
}

func TestRetryDelayExponentialCapped(t *testing.T) {
	base := 1 * time.Second
	max := 3 * time.Second
	for attempt := 0; attempt < 10; attempt++ {
		if got := retryDelay(base, max, attempt, config.Exponential); got > max {
			t.Errorf("attempt %d: delay %v exceeds max %v", attempt, got, max)
		}
	}
	if got := retryDelay(base, max, 2, config.Exponential); got != max {
		t.Errorf("expected cap %v, got %v", max, got)
	}
}

func TestRetryDelayExponentialJitter(t *testing.T) {
	base := 4 * time.Second
	max := 16 * time.Second
	for attempt := 0; attempt < 100; attempt++ {
		got := retryDelay(base, max, attempt, config.ExponentialJitter)
		if got < 0 || got > max {
			t.Errorf("attempt %d: delay %v out of range [0, %v]", attempt, got, max)
		}
	}
}

func TestRetryDelayJitterUsesCap(t *testing.T) {
	base := 100 * time.Second
	max := 2 * time.Second
	for attempt := 0; attempt < 100; attempt++ {
		if got := retryDelay(base, max, attempt, config.ExponentialJitter); got > max {
			t.Errorf("delay %v exceeds cap %v", got, max)
		}
	}
}

func TestRetryDelayZeroBase(t *testing.T) {
	for _, strategy := range []string{config.Constant, config.Linear, config.Exponential, config.ExponentialJitter, "unknown"} {
		if got := retryDelay(0, 0, 0, strategy); got != 0 {
			t.Errorf("strategy %s: expected 0, got %v", strategy, got)
		}
	}
}

func TestRetryDelayUnknownStrategy(t *testing.T) {
	if got := retryDelay(2*time.Second, 0, 0, "bogus"); got != 0 {
		t.Errorf("expected 0 for unknown strategy, got %v", got)
	}
}
