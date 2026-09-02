package poller

import (
	"time"
)

// Sample is one global rate observation backing the sparkline and
// throughput graph.
type Sample struct {
	At       time.Time `json:"at"`
	DownRate int64     `json:"downRate"`
	UpRate   int64     `json:"upRate"`
}

// historyWindow is the rolling buffer length (60 minutes, per the design).
const historyWindow = 60 * time.Minute

// historyCap bounds the buffer when polls are faster than one per second.
const historyCap = 3600

// appendSample appends a sample and prunes anything older than the window.
func appendSample(history []Sample, s Sample, now time.Time) []Sample {
	history = append(history, s)
	cutoff := now.Add(-historyWindow)
	// Drop from the front while the oldest sample is stale.
	drop := 0
	for drop < len(history) && history[drop].At.Before(cutoff) {
		drop++
	}
	if drop > 0 {
		history = append(history[:0], history[drop:]...)
	}
	if len(history) > historyCap {
		history = history[len(history)-historyCap:]
	}
	return history
}

// samplesSince returns the samples within the window ending at now.
func samplesSince(history []Sample, now time.Time) []Sample {
	cutoff := now.Add(-historyWindow)
	for i, s := range history {
		if !s.At.Before(cutoff) {
			return history[i:]
		}
	}
	return nil
}
