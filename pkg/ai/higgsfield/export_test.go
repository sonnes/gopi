package higgsfield

import "time"

// PollInterval sets the wait between two status reads, so a test does
// not wait [pollInterval].
func PollInterval(d time.Duration) Option {
	return func(c *config) { c.pollInterval = d }
}

// PollTimeout sets the longest wait for a final status, so a test does
// not wait [pollTimeout].
func PollTimeout(d time.Duration) Option {
	return func(c *config) { c.pollTimeout = d }
}
