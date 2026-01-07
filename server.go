package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type server struct {
	cfg *config
	log *logger
	out io.Writer // where the command's output goes

	mu     sync.Mutex // held while a command runs, so runs never overlap
	nextID atomic.Int64
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
	return &server{cfg: cfg, log: log, out: log.w}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.reply(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.reply(w, r, http.StatusBadRequest, "reading body: "+err.Error())
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
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "accepted: run #%d\n", j.id)

	go s.runJob(j)
}

func (s *server) runJob(j *job) {
	s.mu.Lock()
	res := s.execute(j)
	s.mu.Unlock()
	s.logRun(j, http.StatusAccepted, res)
}

func (s *server) reply(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	s.log.Printf("%s %s %s %d", r.RemoteAddr, r.Method, r.URL.Path, status)
}

// logRun writes the access log line for a request that ran the command.
func (s *server) logRun(j *job, status int, res result) {
	s.log.Printf("%s %s %s %d #%d %s exit=%d", j.remote, j.method, j.path, status,
		j.id, res.duration.Round(time.Millisecond), res.exitCode)
}
