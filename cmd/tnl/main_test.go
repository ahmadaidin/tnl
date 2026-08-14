package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ahmadaidin/tnl/internal/daemon"
	"github.com/ahmadaidin/tnl/internal/status"
)

func TestRenderStatusOneShot(t *testing.T) {
	var calls int
	var out bytes.Buffer
	err := renderStatus(context.Background(), false, &out, func(context.Context) (*daemon.Response, error) {
		calls++
		return &daemon.Response{Tunnels: []status.TunnelStatus{{Name: "web"}}}, nil
	}, nil)
	if err != nil {
		t.Fatalf("renderStatus() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("query calls = %d, want 1", calls)
	}
	want := "\x1b[90mweb\x1b[0m [\x1b[90m0 mappings\x1b[0m]\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestRenderStatusWatchFramesAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	queried := make(chan int, 2)
	var calls int
	var out bytes.Buffer
	written := make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() {
		done <- renderStatus(ctx, true, notifyWriter{Buffer: &out, Written: written}, func(context.Context) (*daemon.Response, error) {
			calls++
			queried <- calls
			return &daemon.Response{Message: []string{"first", "second"}[calls-1]}, nil
		}, ticks)
	}()

	if got := <-queried; got != 1 {
		t.Fatalf("initial query number = %d, want 1", got)
	}
	<-written
	if got := out.String(); got != "\x1b[H\x1b[2Jfirst\n" {
		t.Fatalf("initial output = %q", got)
	}
	ticks <- time.Now()
	if got := <-queried; got != 2 {
		t.Fatalf("ticked query number = %d, want 2", got)
	}
	<-written
	if got := out.String(); got != "\x1b[H\x1b[2Jfirst\n\x1b[H\x1b[2Jsecond\n" {
		t.Fatalf("ticked output = %q", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("renderStatus() after cancellation error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("query calls after cancellation = %d, want 2", calls)
	}
}

type notifyWriter struct {
	*bytes.Buffer
	Written chan<- struct{}
}

func (w notifyWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(p, []byte("\n")) {
		w.Written <- struct{}{}
	}
	return n, err
}

func TestRenderStatusWatchQueryError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	queried := make(chan int, 2)
	var calls int
	errWant := errors.New("query failed")
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- renderStatus(ctx, true, &out, func(context.Context) (*daemon.Response, error) {
			calls++
			queried <- calls
			if calls == 1 {
				return &daemon.Response{Message: "fresh"}, nil
			}
			return nil, errWant
		}, ticks)
	}()
	<-queried
	ticks <- time.Now()
	<-queried
	if err := <-done; !errors.Is(err, errWant) {
		t.Fatalf("renderStatus() error = %v, want %v", err, errWant)
	}
	if got := out.String(); got != "\x1b[H\x1b[2Jfresh\n" {
		t.Fatalf("output = %q, want only first frame", got)
	}
	cancel()
}

func TestRenderStatusResponseError(t *testing.T) {
	var out bytes.Buffer
	err := renderStatus(context.Background(), true, &out, func(context.Context) (*daemon.Response, error) {
		return &daemon.Response{Error: "daemon failed"}, nil
	}, make(chan time.Time))
	if err == nil || err.Error() != "daemon failed" {
		t.Fatalf("renderStatus() error = %v, want daemon failed", err)
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want no clear or render prefix", out.String())
	}
}
