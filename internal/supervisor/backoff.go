package supervisor

import (
	"math/rand/v2"
	"time"
)

// backoffDelay computes the wait before restart attempt n, growing
// exponentially from base up to cap with multiplicative jitter of ±jitter and
// a result clamped to [0, cap*1.2]. attempt starts at 1; values below 1 are
// treated as 1.
func backoffDelay(base, cap time.Duration, jitter float64, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d <= 0 || d >= cap {
			d = cap
			break
		}
	}
	if d > cap {
		d = cap
	}
	if jitter > 0 {
		u := rand.Float64()*2 - 1 // uniform(-1, 1)
		d += time.Duration(float64(d) * jitter * u)
	}
	if d < 0 {
		d = 0
	}
	if d > cap*6/5 {
		d = cap * 6 / 5
	}
	return d
}
