package dispatcher_test

import (
	"testing"

	"github.com/zt4ff/gokue/config"
	"github.com/zt4ff/gokue/dispatcher"
)

func setup(t *testing.T) *dispatcher.Dispatcher {
	t.Helper()

	config := config.Config{
		Backend:   "in-memory",
		QueueSize: 10,
	}

	dispatcher := dispatcher.New(config, nil)

	return dispatcher
}

func TestXxx(t *testing.T) {
	_ = setup(t)
}
