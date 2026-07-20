package service

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestEngineManagerShutdownIsIdempotentAndRejectsNewJobs(t *testing.T) {
	em := NewEngineManager("", "", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := em.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := em.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if _, err := em.StartTiming("late", TimingParams{}, false); !errors.Is(err, ErrEngineShuttingDown) {
		t.Fatalf("StartTiming error = %v, want ErrEngineShuttingDown", err)
	}
}

type blockingEngineInput struct {
	startOnce sync.Once
	closeOnce sync.Once
	started   chan struct{}
	release   chan struct{}
}

type failingEngineInput struct{}

type recordingProcessTreeAuthority struct {
	mu    sync.Mutex
	kills int
}

func (a *recordingProcessTreeAuthority) Kill() error {
	a.mu.Lock()
	a.kills++
	a.mu.Unlock()
	return nil
}

func (a *recordingProcessTreeAuthority) killCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.kills
}

func (failingEngineInput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (failingEngineInput) Close() error              { return nil }

func TestEngineProcessExitKillsRetainedTreeBeforePublishingExit(t *testing.T) {
	authority := &recordingProcessTreeAuthority{}
	proc := &engineProc{tree: authority, exited: make(chan struct{})}
	callbackSawKill := false
	if !proc.bind(nil, func() { callbackSawKill = authority.killCount() == 1 }) {
		t.Fatal("failed to bind live process")
	}

	// finishExit models the point after the platform has already reaped the
	// leader. The retained authority must still kill descendants exactly once.
	proc.finishExit()
	proc.forceKill()
	if !callbackSawKill {
		t.Fatal("process exit was published before descendant cleanup")
	}
	if got := authority.killCount(); got != 1 {
		t.Fatalf("tree cleanup calls = %d, want 1", got)
	}
	select {
	case <-proc.exited:
	default:
		t.Fatal("process exit was not published")
	}
}

func (w *blockingEngineInput) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *blockingEngineInput) Close() error {
	w.closeOnce.Do(func() { close(w.release) })
	return nil
}

func TestEngineManagerShutdownDoesNotWaitForBlockedWriteMutex(t *testing.T) {
	input := &blockingEngineInput{started: make(chan struct{}), release: make(chan struct{})}
	proc := &engineProc{stdin: input, exited: make(chan struct{})}
	em := NewEngineManager("", "", "")
	em.procs[proc] = struct{}{}
	job := &EngineTimingJob{TaskID: "blocked", Status: "running", proc: proc}
	em.timingJobs[job.TaskID] = job

	requestDone := make(chan struct{})
	go func() {
		_, _ = proc.request(context.Background(), "blocked", nil)
		close(requestDone)
	}()
	select {
	case <-input.started:
	case <-time.After(time.Second):
		t.Fatal("request did not enter blocked stdin write")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := em.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Shutdown blocked for %s", elapsed)
	}
	select {
	case <-input.release:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not close blocked stdin")
	}
	proc.onExit()
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not unwind")
	}
}

func TestCloseTimingWaitsForDocumentOperationBeforeRecycling(t *testing.T) {
	em := NewEngineManager("", "", "")
	proc := &engineProc{exited: make(chan struct{})}
	job := &EngineTimingJob{TaskID: "document", Status: "done", proc: proc}
	em.timingJobs[job.TaskID] = job
	em.timingOrder = []string{job.TaskID}

	job.DocumentMu.Lock()
	done := make(chan error, 1)
	go func() { done <- em.CloseTiming(job.TaskID) }()
	select {
	case err := <-done:
		t.Fatalf("CloseTiming returned before document operation ended: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	em.mu.Lock()
	spare := em.spare
	em.mu.Unlock()
	if spare != nil {
		t.Fatal("process was recycled while a document operation still owned it")
	}
	job.DocumentMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseTiming remained blocked")
	}
	em.mu.Lock()
	spare = em.spare
	em.mu.Unlock()
	if spare != proc {
		t.Fatal("terminal timing process was not recycled after document operation")
	}
}

func TestSerialTimingReplacementWaitsForDocumentOperationBeforeRecycling(t *testing.T) {
	em := NewEngineManager("", "", "")
	proc := &engineProc{stdin: failingEngineInput{}, exited: make(chan struct{})}
	oldJob := &EngineTimingJob{TaskID: "old", Status: "done", proc: proc}
	em.timingJobs[oldJob.TaskID] = oldJob
	em.timingOrder = []string{oldJob.TaskID}

	oldJob.DocumentMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := em.StartTiming("new", TimingParams{}, false)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("serial replacement returned before document operation ended: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	em.mu.Lock()
	spare := em.spare
	em.mu.Unlock()
	if spare != nil {
		t.Fatal("serial replacement recycled a process still used by a document operation")
	}
	oldJob.DocumentMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("serial replacement remained blocked")
	}
}

func TestCancelRecordsIntentWithAssignedProcess(t *testing.T) {
	tests := []struct {
		domain string
		add    func(*EngineManager, *engineProc)
		state  func(*EngineManager) string
		finish func(*EngineManager)
	}{
		{
			domain: "timing",
			add: func(em *EngineManager, proc *engineProc) {
				em.timingJobs["task"] = &EngineTimingJob{TaskID: "task", Status: "running", proc: proc}
			},
			state: func(em *EngineManager) string {
				job := em.timingJobs["task"]
				job.Mu.Lock()
				defer job.Mu.Unlock()
				if job.cancelRequested {
					return "requested"
				}
				return job.Status
			},
			finish: func(em *EngineManager) {
				routeTimingNotification(em.timingJobs["task"], "subtitle.finished", []byte(`{"Reason":"Completed"}`))
			},
		},
		{
			domain: "suppress",
			add: func(em *EngineManager, proc *engineProc) {
				em.suppressJobs["task"] = &EngineSuppressJob{TaskID: "task", Status: "running", proc: proc}
			},
			state: func(em *EngineManager) string {
				job := em.suppressJobs["task"]
				job.Mu.Lock()
				defer job.Mu.Unlock()
				if job.cancelRequested {
					return "requested"
				}
				return job.Status
			},
			finish: func(em *EngineManager) {
				em.routeSuppressNotification(em.suppressJobs["task"], "suppress.finished", []byte(`{"Reason":"Completed"}`))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.domain, func(t *testing.T) {
			em := NewEngineManager("", "", "")
			proc := &engineProc{dead: true, exited: make(chan struct{})}
			tc.add(em, proc)
			_ = em.Cancel(tc.domain, "task")
			if got := tc.state(em); got != "requested" {
				t.Fatalf("status = %q, want cancellation intent", got)
			}
			tc.finish(em)
			if got := tc.state(em); got != "canceled" {
				t.Fatalf("terminal status = %q, cancellation intent was overwritten", got)
			}
		})
	}
}
