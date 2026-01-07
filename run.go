package main

import (
	"bytes"
	"errors"
	"os/exec"
	"time"
)

type result struct {
	exitCode int
	duration time.Duration
}

// execute runs the configured command for j and waits for it to finish.
func (s *server) execute(j *job) result {
	cmd := exec.Command(s.cfg.argv[0], s.cfg.argv[1:]...)
	cmd.Env = j.env
	cmd.Stdin = bytes.NewReader(j.body)
	cmd.Stdout = s.out
	cmd.Stderr = s.out

	start := time.Now()
	err := cmd.Run()
	res := result{duration: time.Since(start)}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exitCode = ee.ExitCode()
		} else {
			// The command could not be started at all.
			s.log.Printf("#%d: %v", j.id, err)
			res.exitCode = 127
		}
	}
	return res
}
