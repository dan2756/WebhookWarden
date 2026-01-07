package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// logger writes timestamped lines to w. It is safe for concurrent use, so
// access log lines from different requests never interleave.
type logger struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func newLogger(w io.Writer) *logger {
	return &logger{w: w, now: time.Now}
}

func (l *logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %s\n", l.now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
