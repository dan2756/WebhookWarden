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

// Printf writes one timestamped line. Characters that start a new line are
// escaped so one event stays one line: a request path is decoded before it
// is logged, and a %0a in that path would otherwise split the line in two.
func (l *logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %s\n", l.now().Format(time.RFC3339), oneLine(fmt.Sprintf(format, args...)))
}

// breaksLogLine reports whether r starts a new line, or is a C1 control a
// terminal would act on, when the log is written out. ASCII controls are
// the usual case; U+0085 (NEL) and the Unicode line separators do the same.
func breaksLogLine(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029
}

// oneLine escapes characters that would break s out of one log line.
func oneLine(s string) string {
	if !strings.ContainsFunc(s, breaksLogLine) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case breaksLogLine(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
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
