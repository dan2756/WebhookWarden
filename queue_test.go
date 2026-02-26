package main

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// waitFor fails the test if wg isn't done within a few seconds.
func waitFor(t *testing.T, wg *sync.WaitGroup, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestQueueRunsOneAtATimeInOrder(t *testing.T) {
	gate := make(chan struct{})
	var (
		mu        sync.Mutex
		order     []int64
		active    int
		maxActive int
	)
	q := newQueue(1, 0, func(j *job) {
		mu.Lock()
		active++
		maxActive = max(maxActive, active)
		mu.Unlock()
		if j.id == 1 {
			<-gate // hold the first run so the others pile up behind it
		}
		mu.Lock()
		order = append(order, j.id)
		active--
		mu.Unlock()
	})

	for range 5 {
		if err := q.submit(&job{}); err != nil {
			t.Fatal(err)
		}
	}
	if running, waiting := q.stats(); running != 1 || waiting != 4 {
		t.Errorf("stats = %d running, %d waiting; want 1, 4", running, waiting)
	}
	close(gate)
	q.wait()

	if want := []int64{1, 2, 3, 4, 5}; !slices.Equal(order, want) {
		t.Errorf("ran in order %v, want %v", order, want)
	}
	if maxActive != 1 {
		t.Errorf("%d runs overlapped", maxActive)
	}
	if running, waiting := q.stats(); running != 0 || waiting != 0 {
		t.Errorf("after wait: %d running, %d waiting", running, waiting)
	}
}

func TestQueueConcurrent(t *testing.T) {
	var started sync.WaitGroup
	started.Add(3)
	release := make(chan struct{})
	q := newQueue(0, 0, func(*job) {
		started.Done()
		<-release
	})
	for range 3 {
		if err := q.submit(&job{}); err != nil {
			t.Fatal(err)
		}
	}
	// With a limit of one this would never finish.
	waitFor(t, &started, "all three runs to start")
	if running, waiting := q.stats(); running != 3 || waiting != 0 {
		t.Errorf("stats = %d running, %d waiting; want 3, 0", running, waiting)
	}
	close(release)
	q.wait()
}

func TestQueueFull(t *testing.T) {
	for _, maxRunning := range []int{1, 0} {
		release := make(chan struct{})
		q := newQueue(maxRunning, 2, func(*job) { <-release })

		a, b, c := &job{}, &job{}, &job{}
		if err := q.submit(a); err != nil {
			t.Fatal(err)
		}
		if err := q.submit(b); err != nil {
			t.Fatal(err)
		}
		if err := q.submit(c); err != errQueueFull {
			t.Errorf("maxRunning=%d: third submit = %v, want errQueueFull", maxRunning, err)
		}
		close(release)
		q.wait()

		if err := q.submit(c); err != nil {
			t.Errorf("maxRunning=%d: submit after drain: %v", maxRunning, err)
		}
		q.wait()
		if a.id != 1 || b.id != 2 || c.id != 3 {
			t.Errorf("maxRunning=%d: ids %d %d %d, want 1 2 3 (rejected jobs don't use up a number)",
				maxRunning, a.id, b.id, c.id)
		}
	}
}
