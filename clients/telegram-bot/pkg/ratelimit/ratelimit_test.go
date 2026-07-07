package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLimiter_AllowsBurstImmediately(t *testing.T) {
	lim := New(1, 3)
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := lim.Wait(ctx); err != nil {
			t.Fatalf("Wait call %d returned error: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("burst of 3 (limit allows burst=3) took %v, want ~immediate", elapsed)
	}
}

func TestLimiter_ThrottlesBeyondBurst(t *testing.T) {
	lim := New(20, 1) // 1 token burst, refilling at 20/s (~50ms/token)
	ctx := context.Background()

	if err := lim.Wait(ctx); err != nil {
		t.Fatalf("first Wait returned error: %v", err)
	}

	start := time.Now()
	if err := lim.Wait(ctx); err != nil {
		t.Fatalf("second Wait returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("second Wait returned in %v, want it to block for a refill (~50ms)", elapsed)
	}
}

func TestLimiter_ContextCancellationStopsWaiting(t *testing.T) {
	lim := New(1, 1)
	ctx := context.Background()
	_ = lim.Wait(ctx) // consume the only burst token

	cancelCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()

	if err := lim.Wait(cancelCtx); err == nil {
		t.Error("Wait should return an error once the context is cancelled while waiting for a token")
	}
}
