// Package main shows an HTTP server that hands slow work to a gokue queue so
// requests return immediately.
//
// POST /signup?email=a@example.com queues a welcome email and returns 202.
// When the queue is full the handler returns 503 instead of blocking the
// request. GET /stats reports queue statistics as JSON. SIGINT or SIGTERM
// stops the server, then drains the queue.
//
//	go run ./examples/http-offload
//	curl -X POST 'localhost:8080/signup?email=a@example.com'
//	curl localhost:8080/stats
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zt4ff/gokue/v2"
)

// WelcomeEmail is the slow work done after a signup.
type WelcomeEmail struct {
	To string
}

// Process implements gokue.Job.
func (e WelcomeEmail) Process(ctx context.Context) error {
	select {
	case <-time.After(time.Second): // stands in for an email API call
		log.Printf("sent welcome email to %s", e.To)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	runFor := flag.Duration("run", 0, "stop after this long without a signal (0 waits for a signal)")
	flag.Parse()

	queue, err := gokue.NewQueue(
		gokue.WithWorkerCount(4),
		gokue.WithQueueSize(100),
		gokue.WithMaxRetries(3),
		gokue.WithRetryDelay(500*time.Millisecond),
		gokue.WithJobTimeout(10*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/signup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		email := r.URL.Query().Get("email")
		if email == "" {
			http.Error(w, "email is required", http.StatusBadRequest)
			return
		}
		// TrySubmit never blocks, so a burst of signups cannot tie up
		// request goroutines. The job does not use the request context:
		// it keeps running after the response is sent.
		switch err := queue.TrySubmit(r.Context(), WelcomeEmail{To: email}); {
		case err == nil:
			w.WriteHeader(http.StatusAccepted)
		case errors.Is(err, gokue.ErrQueueFull):
			w.Header().Set("Retry-After", "1")
			http.Error(w, "busy, try again", http.StatusServiceUnavailable)
		case errors.Is(err, gokue.ErrClosed):
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(queue.Stats())
	})

	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *runFor)
		defer cancel()
	}

	go func() {
		log.Printf("listening on %s", *addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()

	// Stop taking requests first so no new jobs arrive, then drain the queue.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if err := queue.Close(shutdownCtx); err != nil {
		log.Printf("queue drain: %v", err)
	}
	log.Printf("stopped: %+v", queue.Stats())
}
