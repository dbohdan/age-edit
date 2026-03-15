package main

import (
	"fmt"
	"os"
	"time"
)

// handleAutosave sets up a timer that calls the save function at regular intervals.
// The interval is specified by the duration parameter.
// It returns a stop function that should be called to clean up the timer.
// If duration is zero or negative, it returns a no-op stop function.
func handleAutosave(duration time.Duration, save func() error) func() {
	if duration <= 0 {
		return func() {}
	}

	ticker := time.NewTicker(duration)
	stop := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				if err := save(); err != nil {
					fmt.Fprintf(os.Stderr, "\r\007age-edit: autosave failed: %v\n", err)
				}

			case <-stop:
				ticker.Stop()

				return
			}
		}
	}()

	return func() {
		close(stop)
	}
}
