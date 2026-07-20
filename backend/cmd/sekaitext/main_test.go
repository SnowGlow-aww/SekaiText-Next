package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeLifecycleShutdowner struct {
	called bool
	err    error
}

func (s *fakeLifecycleShutdowner) Shutdown(context.Context) error {
	s.called = true
	return s.err
}

func TestShutdownLifecycleAlwaysInvokesBackendCleanup(t *testing.T) {
	wantErr := errors.New("cleanup failed")
	backend := &fakeLifecycleShutdowner{err: wantErr}
	err := shutdownLifecycle(backend, nil, time.Second)
	if !backend.called {
		t.Fatal("backend cleanup was not invoked")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("shutdownLifecycle error = %v, want %v", err, wantErr)
	}
}
