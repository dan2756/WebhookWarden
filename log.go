package main

import (
	"fmt"
	"io"
	"strings"
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

// Printf writes one timestamped line. Control characters in the message are
// escaped so one event stays one line: a request path is decoded before it
// is logged, and a %0a in that path would otherwise split the line in two.
func (l *logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %s\n", l.now().Format(time.RFC3339), oneLine(fmt.Sprintf(format, args...)))
}

// oneLine escapes ASCII control characters so s cannot break out of a log line.
func oneLine(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// Line writes prefix and b as one line, without a timestamp. It is used to
// forward the command's own output.
func (l *logger) Line(prefix string, b []byte) {
	line := make([]byte, 0, len(prefix)+len(b)+1)
	line = append(line, prefix...)
	line = append(line, b...)
	line = append(line, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(line)
}
