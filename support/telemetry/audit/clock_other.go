//go:build !linux

package audit

import "time"

var origin = time.Now()

// Only used by non-Linux unit tests; Open rejects unsupported production hosts.
func Now() int64 { return time.Since(origin).Nanoseconds() }
