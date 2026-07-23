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

func TestAuthTokenForTransport(t *testing.T) {
	for _, tt := range []struct {
		name    string
		ipc     bool
		token   string
		want    string
		wantErr bool
	}{
		{name: "TCP rejects empty token", token: "", wantErr: true},
		{name: "TCP rejects whitespace token", token: "  ", wantErr: true},
		{name: "TCP keeps non-empty token", token: "secret-token", want: "secret-token"},
		{name: "IPC does not expose supplied token", ipc: true, token: "ignored", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := authTokenForTransport(tt.ipc, tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("token = %q, want %q", got, tt.want)
			}
		})
	}
}
