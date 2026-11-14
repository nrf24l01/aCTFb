package boards

import "time"

// SystemClock is the production UTC clock.
type SystemClock struct{}

// Now returns the current UTC instant.
func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
