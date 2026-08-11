package supervisor

import (
	"testing"
	"time"
)

func TestBackoffDelayAttemptOne(t *testing.T) {
	base, cap := time.Second, 60*time.Second
	if got := backoffDelay(base, cap, 0, 1); got != base {
		t.Fatalf("attempt 1 = %v, want %v", got, base)
	}
	// Attempts below 1 are treated as 1 (e.g. the port-in-use path).
	if got := backoffDelay(base, cap, 0, 0); got != base {
		t.Fatalf("attempt 0 = %v, want %v", got, base)
	}
	if got := backoffDelay(base, cap, 0, -3); got != base {
		t.Fatalf("attempt -3 = %v, want %v", got, base)
	}
}

func TestBackoffDelayExponentialAndCap(t *testing.T) {
	base, cap := time.Second, 10*time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, cap}, // 16s exceeds the cap
		{100, cap},
	}
	for _, c := range cases {
		if got := backoffDelay(base, cap, 0, c.attempt); got != c.want {
			t.Fatalf("attempt %d = %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestBackoffDelayJitterWithinBounds(t *testing.T) {
	base, cap := time.Second, 10*time.Second
	const jitter = 0.2
	const slack = 2 // nanoseconds of float rounding slack
	for attempt := 1; attempt <= 8; attempt++ {
		d := base
		for i := 1; i < attempt && d < cap; i++ {
			d *= 2
		}
		if d > cap {
			d = cap
		}
		lo := time.Duration(float64(d)*(1-jitter)) - slack
		hi := time.Duration(float64(d)*(1+jitter)) + slack
		if hi > cap*6/5 {
			hi = cap*6/5 + slack
		}
		for range 200 {
			got := backoffDelay(base, cap, jitter, attempt)
			if got < lo || got > hi {
				t.Fatalf("attempt %d: delay %v outside [%v, %v]", attempt, got, lo, hi)
			}
		}
	}
}

func TestBackoffDelayClampedToCapTimesJitter(t *testing.T) {
	base, cap := time.Second, 10*time.Second
	// Heavy jitter at the cap: 10s + up to 50% = 15s, clamped to 12s.
	for range 500 {
		got := backoffDelay(base, cap, 0.5, 50)
		if got < 0 || got > cap*6/5 {
			t.Fatalf("delay %v outside [0, %v]", got, cap*6/5)
		}
	}
}
