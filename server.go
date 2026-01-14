package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type server struct {
	cfg *config
	log *logger

	runMu  sync.Mutex // held while a command runs, so runs never overlap
	wg     sync.WaitGroup
	nextID atomic.Int64

	mu       sync.Mutex // guards the fields below
	runs     int
	lastExit int
	lastRun  time.Time
}

// job is one accepted request waiting to run.
type job struct {
	id     int64
	remote string
	method string
	path   string
	body   []byte
	env    []string
}

func newServer(cfg *config, log *logger) *server {
	return &server{cfg: cfg, log: log}
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.reply(w, r, http.StatusBadRequest, "reading body: "+err.Error())
		return
	}
	if s.cfg.secret != "" && !verifySignature([]byte(s.cfg.secret), r.Header, body) {
		s.reply(w, r, http.StatusUnauthorized, "invalid or missing signature")
		return
	}

	j := &job{
		id:     s.nextID.Add(1),
		remote: r.RemoteAddr,
		method: r.Method,
		path:   r.URL.Path,
		body:   body,
		env:    buildEnv(os.Environ(), r),
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Hook-Run", strconv.FormatInt(j.id, 10))

	if !s.cfg.sync {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "accepted: run #%d\n", j.id)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.logRun(j, http.StatusAccepted, s.runJob(j))
		}()
		return
	}

	res := s.runJob(j)
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

// runJob runs j once no other run is in progress and records the result.
func (s *server) runJob(j *job) result {
	s.runMu.Lock()
	res := s.execute(j)
	s.runMu.Unlock()
	s.record(res)
	return res
}

// wait blocks until all background runs have finished.
func (s *server) wait() { s.wg.Wait() }

func (s *server) record(res result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs++
	s.lastExit = res.exitCode
	s.lastRun = time.Now()
}

type statusReport struct {
	Runs         int        `json:"runs"`
	LastExitCode *int       `json:"last_exit_code"`
	LastRun      *time.Time `json:"last_run"`
}

func (s *server) status() statusReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := statusReport{Runs: s.runs}
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
