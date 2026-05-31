package main

import (
	"context"
	"errors"
	"sync"
)

var (
	errQueueFull = errors.New("queue full")
	errClosed    = errors.New("shutting down")
)

// queue runs jobs in the order they were submitted, at most maxRunning at
// a time (0 means no limit), and refuses new jobs once maxPending are
// running or waiting (0 means no limit).
type queue struct {
	run        func(*job)
	maxRunning int
	maxPending int

	mu      sync.Mutex
	seq     int64
	running int
	waiting []*job
	closed  bool
	wg      sync.WaitGroup
}

func newQueue(maxRunning, maxPending int, run func(*job)) *queue {
	return &queue{run: run, maxRunning: maxRunning, maxPending: maxPending}
}

// submit numbers j and schedules it, or returns errQueueFull or errClosed.
func (q *queue) submit(j *job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errClosed
	}
	if q.maxPending > 0 && q.running+len(q.waiting) >= q.maxPending {
		return errQueueFull
	}
	q.seq++
	j.id = q.seq
	if q.maxRunning > 0 && q.running >= q.maxRunning {
		q.waiting = append(q.waiting, j)
		return nil
	}
	q.running++
	q.wg.Add(1)
	go q.work(j)
	return nil
}

// work runs j, then keeps taking waiting jobs until there are none left.
// Handing the slot straight to the next job keeps the order FIFO.
func (q *queue) work(j *job) {
	defer q.wg.Done()
	for j != nil {
		q.run(j)

		q.mu.Lock()
		j = nil
		if len(q.waiting) > 0 {
			j = q.waiting[0]
			q.waiting[0] = nil
			q.waiting = q.waiting[1:]
		} else {
			q.running--
		}
		q.mu.Unlock()
	}
}

// stats returns how many jobs are running and how many are waiting.
func (q *queue) stats() (running, waiting int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running, len(q.waiting)
}

// close makes every later submit fail and returns the jobs that were still
// waiting. They are removed from the queue and will not run.
func (q *queue) close() []*job {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	dropped := q.waiting
	q.waiting = nil
	return dropped
}

// wait blocks until every submitted job has finished, or ctx is done.
func (q *queue) wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
