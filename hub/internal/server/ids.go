package server

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// NewID returns a monotonic ULID for use as a primary key.
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// NowUTC returns an RFC3339Nano-formatted UTC timestamp for DB writes.
func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
