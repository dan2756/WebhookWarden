package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const (
	// maxCapture bounds how much output --sync keeps for the response.
	maxCapture = 1 << 20
	// maxLine bounds how long a forwarded output line can get before it is
	// flushed anyway.
	maxLine = 64 << 10
)

type result struct {
	exitCode  int
	duration  time.Duration
	output    []byte // combined stdout and stderr, only with --sync
	truncated bool
}

// execute runs the configured command for j and waits for it to finish.
func (s *server) execute(j *job) result {
	cmd := exec.Command(s.cfg.argv[0], s.cfg.argv[1:]...)
	cmd.Env = j.env
	cmd.Stdin = bytes.NewReader(j.body)

	var writers []io.Writer
	var capture *capBuffer
	if s.cfg.sync {
		capture = &capBuffer{max: maxCapture}
		writers = append(writers, capture)
	}
	var fwd *prefixWriter
	if !s.cfg.quiet {
		fwd = &prefixWriter{log: s.log, prefix: fmt.Sprintf("[#%d] ", j.id)}
		writers = append(writers, fwd)
	}
	switch len(writers) {
	case 1:
		cmd.Stdout = writers[0]
	case 2:
		cmd.Stdout = io.MultiWriter(writers...)
	}
	// Using the same writer for both makes exec share one pipe, which keeps
	// stdout and stderr in the order the command wrote them.
	cmd.Stderr = cmd.Stdout

	start := time.Now()
	err := cmd.Run()
	res := result{duration: time.Since(start)}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exitCode = ee.ExitCode()
		} else {
			// The command could not be started at all.
			res.exitCode = 127
			if cmd.Stdout != nil {
				fmt.Fprintf(cmd.Stdout, "hook: %v\n", err)
			} else {
				s.log.Printf("#%d: %v", j.id, err)
			}
		}
	}
	if fwd != nil {
		fwd.Flush()
	}
	if capture != nil {
		res.output, res.truncated = capture.buf.Bytes(), capture.truncated
	}
	return res
}

// capBuffer keeps the first max bytes written to it and drops the rest.
type capBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); len(p) > room {
		b.truncated = true
		b.buf.Write(p[:max(room, 0)])
		return len(p), nil
	}
	return b.buf.Write(p)
}

// prefixWriter forwards complete lines to the log, each prefixed with the
// run number so that output from different runs can be told apart.
type prefixWriter struct {
	log    *logger
	prefix string
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.log.Line(w.prefix, w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) >= maxLine {
		w.log.Line(w.prefix, w.buf)
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// Flush writes out a last line that had no trailing newline.
func (w *prefixWriter) Flush() {
	if len(w.buf) > 0 {
		w.log.Line(w.prefix, w.buf)
		w.buf = nil
	}
}
