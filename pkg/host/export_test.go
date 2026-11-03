package host

// SetAfterStart makes fn run in Send and Compact right after the start
// of the agent, and returns a function that removes it.
func SetAfterStart(fn func()) func() {
	old := afterStart
	afterStart = fn
	return func() { afterStart = old }
}
