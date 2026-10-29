package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartupStopsWaitingWhenServiceExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exited := make(chan error, 1)
	exited <- errors.New("exit status 1")
	err := awaitService(ctx, func(context.Context) error {
		t.Fatal("a service that already exited must not be polled")
		return nil
	}, exited, io.Discard)
	require.ErrorContains(t, err, "service exited during startup")
	require.ErrorContains(t, err, "exit status 1")
}

func TestStartupCancellationInterruptsReadinessRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probing := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- awaitService(ctx, func(probe context.Context) error {
			close(probing)
			<-probe.Done()
			return probe.Err()
		}, make(chan error), io.Discard)
	}()
	select {
	case <-probing:
	case <-time.After(time.Second):
		t.Fatal("startup never probed the service")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("startup ignored cancellation")
	}
}

func TestStartupContinuesOnceServiceIsReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := awaitService(ctx, func(context.Context) error { return nil }, make(chan error), io.Discard)
	require.NoError(t, err)
}

func TestConcurrentStartupWaitsAndCanBeCancelled(t *testing.T) {
	root := t.TempDir()
	first, err := lockStartup(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { first.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = lockStartup(ctx, root)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, first.Close())
	next, err := lockStartup(context.Background(), root)
	require.NoError(t, err)
	require.NoError(t, next.Close())
}
