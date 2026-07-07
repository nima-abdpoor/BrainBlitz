package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Policy{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Policy{MaxAttempts: 5, BaseDelay: time.Millisecond}, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_ExhaustsAttemptsAndReturnsLastError(t *testing.T) {
	calls := 0
	wantErr := errors.New("always fails")
	err := Do(context.Background(), Policy{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("Do error = %v, want %v", err, wantErr)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (MaxAttempts, not MaxAttempts+1)", calls)
	}
}

func TestDo_NeverRetriesWhenMaxAttemptsIsOne(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Policy{MaxAttempts: 1, BaseDelay: time.Millisecond}, func() error {
		calls++
		return errors.New("fails")
	})
	if err == nil {
		t.Fatal("Do should return an error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDo_BackoffGrowsExponentially(t *testing.T) {
	var timestamps []time.Time
	_ = Do(context.Background(), Policy{MaxAttempts: 4, BaseDelay: 20 * time.Millisecond}, func() error {
		timestamps = append(timestamps, time.Now())
		return errors.New("fail")
	})
	if len(timestamps) != 4 {
		t.Fatalf("got %d attempts, want 4", len(timestamps))
	}
	d1 := timestamps[1].Sub(timestamps[0])
	d2 := timestamps[2].Sub(timestamps[1])
	d3 := timestamps[3].Sub(timestamps[2])
	// Nominal delays are 20ms, 40ms, 80ms — assert rough doubling rather
	// than exact timing to avoid flakiness under CI scheduling jitter.
	if d2 < d1 {
		t.Errorf("delay did not grow: d1=%v d2=%v", d1, d2)
	}
	if d3 < d2 {
		t.Errorf("delay did not grow: d2=%v d3=%v", d2, d3)
	}
}

func TestDo_MaxDelayCapsBackoff(t *testing.T) {
	var timestamps []time.Time
	_ = Do(context.Background(), Policy{MaxAttempts: 4, BaseDelay: 20 * time.Millisecond, MaxDelay: 25 * time.Millisecond}, func() error {
		timestamps = append(timestamps, time.Now())
		return errors.New("fail")
	})
	if len(timestamps) != 4 {
		t.Fatalf("got %d attempts, want 4", len(timestamps))
	}
	// Uncapped delays would be 20, 40, 80ms; capped at 25ms every wait after
	// the first should be close to 25ms, not doubling further.
	d3 := timestamps[3].Sub(timestamps[2])
	if d3 > 60*time.Millisecond {
		t.Errorf("third delay = %v, want capped near MaxDelay (25ms), not the uncapped 80ms", d3)
	}
}

func TestDo_ContextCancelledDuringWaitStopsRetrying(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	calls := 0
	err := Do(ctx, Policy{MaxAttempts: 10, BaseDelay: 50 * time.Millisecond}, func() error {
		calls++
		return errors.New("fail")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Do error = %v, want context.DeadlineExceeded", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (cancelled before the first retry's wait completes)", calls)
	}
}
