package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type server struct {
	cfg   *config
	log   *logger
	queue *queue
	exec  func(context.Context, *job) result // runs the command; replaced in tests

	runCtx context.Context    // parent of every run's context
	kill   context.CancelFunc // cancels runCtx, killing running commands

	mu       sync.Mutex // guards the fields below
	runs     int
	lastExit int
	lastRun  time.Time
}

// job is one accepted request waiting to run.
type job struct {
	id     int64 // assigned by the queue
	remote string
	method string
	path   string
	body   []byte
	env    []string
	done   chan result // set for --sync requests; closed if the job is dropped
}

func newServer(cfg *config, log *logger) *server {
	s := &server{cfg: cfg, log: log}
	s.exec = s.execute
	s.runCtx, s.kill = context.WithCancel(context.Background())
	workers := 1
	if cfg.concurrent {
		workers = 0
	}
	s.queue = newQueue(workers, cfg.maxQueue, s.runJob)
	return s
}

// statusPath is where GET returns the status document.
func (s *server) statusPath() string {
	if s.cfg.path != "" {
		return s.cfg.path
	}
	return "/"
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.cfg.path != "" && r.URL.Path != s.cfg.path {
		s.reply(w, r, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		onStatusPath := r.URL.Path == s.statusPath()
		if onStatusPath && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			s.serveStatus(w, r)
			return
		}
		if onStatusPath {
			w.Header().Set("Allow", "GET, HEAD, POST")
		} else {
			w.Header().Set("Allow", http.MethodPost)
		}
		s.reply(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if s.cfg.maxBody > 0 {
		if r.ContentLength > s.cfg.maxBody {
			s.reply(w, r, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.maxBody)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.reply(w, r, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			s.reply(w, r, http.StatusBadRequest, "reading body: "+err.Error())
		}
		return
	}
	if s.cfg.secret != "" && !verifySignature([]byte(s.cfg.secret), r.Header, body) {
		s.reply(w, r, http.StatusUnauthorized, "invalid or missing signature")
		return
	}

	j := &job{
		remote: r.RemoteAddr,
		method: r.Method,
		path:   r.URL.Path,
		body:   body,
		env:    buildEnv(os.Environ(), r),
	}
	if s.cfg.sync {
		j.done = make(chan result, 1)
	}
	if err := s.queue.submit(j); err != nil {
		s.reply(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Hook-Run", strconv.FormatInt(j.id, 10))

	if !s.cfg.sync {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "accepted: run #%d\n", j.id)
		return
	}

	res, ok := <-j.done
	if !ok {
		s.reply(w, r, http.StatusServiceUnavailable, "shutting down")
		return
	}
	code := http.StatusOK
	if res.exitCode != 0 {
		code = http.StatusInternalServerError
	}
	w.Header().Set("X-Hook-Exit-Code", strconv.Itoa(res.exitCode))
	w.WriteHeader(code)
	w.Write(res.output)
	if res.truncated {
		fmt.Fprintf(w, "\nhook: output truncated to %d bytes\n", maxCapture)
	}
	s.logRun(j, code, res)
}

// runJob is called by the queue when it is j's turn.
func (s *server) runJob(j *job) {
	res := s.exec(s.runCtx, j)
	s.record(res)
	if j.done != nil {
		j.done <- res // the handler logs and responds
		return
	}
	s.logRun(j, http.StatusAccepted, res)
}

// wait blocks until all queued runs have finished.
func (s *server) wait() { s.queue.wait(context.Background()) }

// shutdown stops taking new runs, drops the ones that haven't started and
// waits for the running ones (and the --sync requests waiting on them) to
// finish. If ctx is done first, the running commands are killed. hs may be
// nil.
func (s *server) shutdown(ctx context.Context, hs *http.Server) error {
	for _, j := range s.queue.close() {
		if j.done != nil {
			close(j.done) // its handler answers 503
		} else {
			s.log.Printf("%s %s %s %d #%d dropped, shutting down", j.remote, j.method, j.path,
				http.StatusAccepted, j.id)
		}
	}
	if hs != nil {
		// Stops listening, then waits for in-flight requests.
		hs.Shutdown(ctx)
	}
	if err := s.queue.wait(ctx); err == nil {
		return nil
	}

	s.kill()
	// Killed commands exit at once; give them a moment to be reaped and
	// their requests a chance to be answered.
	wctx, cancel := context.WithTimeout(context.Background(), waitDelay+time.Second)
	defer cancel()
	s.queue.wait(wctx)
	if hs != nil {
		hs.Close()
	}
	return errors.New("running commands did not finish in time and were killed")
}

func (s *server) record(res result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs++
	s.lastExit = res.exitCode
	s.lastRun = time.Now()
}

type statusReport struct {
	Runs         int        `json:"runs"`
	Running      int        `json:"running"`
	Queued       int        `json:"queued"`
	LastExitCode *int       `json:"last_exit_code"`
	LastRun      *time.Time `json:"last_run"`
}

func (s *server) status() statusReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := statusReport{Runs: s.runs}
	st.Running, st.Queued = s.queue.stats()
	if s.runs > 0 {
		code, at := s.lastExit, s.lastRun.UTC().Truncate(time.Second)
		st.LastExitCode, st.LastRun = &code, &at
	}
	return st
}

func (s *server) serveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(s.status())
	s.logRequest(r, http.StatusOK)
}

func (s *server) reply(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	s.logRequest(r, status)
}

func (s *server) logRequest(r *http.Request, status int) {
	s.log.Printf("%s %s %s %d", r.RemoteAddr, r.Method, r.URL.Path, status)
}

// logRun writes the access log line for a request that ran the command.
func (s *server) logRun(j *job, status int, res result) {
	s.log.Printf("%s %s %s %d #%d %s exit=%d", j.remote, j.method, j.path, status,
		j.id, res.duration.Round(time.Millisecond), res.exitCode)
}
