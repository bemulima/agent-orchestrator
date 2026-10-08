package activities

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeriodicShardHeartbeatRunsUntilStopped(t *testing.T) {
	var count atomic.Int32
	heartbeats := make(chan struct{}, 10)
	stop := startShardHeartbeats(context.Background(), time.Millisecond, func() {
		count.Add(1)
		select {
		case heartbeats <- struct{}{}:
		default:
		}
	})
	defer stop()
	for i := 0; i < 2; i++ {
		select {
		case <-heartbeats:
		case <-time.After(time.Second):
			t.Fatal("periodic heartbeat did not run")
		}
	}
	stop()
	stoppedCount := count.Load()
	time.Sleep(5 * time.Millisecond)
	if count.Load() != stoppedCount {
		t.Fatal("heartbeat continued after stop returned")
	}
}

func TestPeriodicShardHeartbeatStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var count atomic.Int32
	heartbeats := make(chan struct{}, 10)
	stop := startShardHeartbeats(ctx, time.Millisecond, func() {
		count.Add(1)
		select {
		case heartbeats <- struct{}{}:
		default:
		}
	})
	defer stop()
	select {
	case <-heartbeats:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not run before cancellation")
	}
	cancel()
	stop()
	stoppedCount := count.Load()
	time.Sleep(5 * time.Millisecond)
	if count.Load() != stoppedCount {
		t.Fatal("heartbeat continued after cancellation")
	}
}

func TestPeriodicShardHeartbeatDoesNotRunForCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var count atomic.Int32
	stop := startShardHeartbeats(ctx, time.Millisecond, func() { count.Add(1) })
	time.Sleep(5 * time.Millisecond)
	stop()
	if count.Load() != 0 {
		t.Fatal("heartbeat ran for cancelled context")
	}
}
