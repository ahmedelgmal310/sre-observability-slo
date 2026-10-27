// Package latency simulates the service time of real work (database calls,
// serialization) so traces and histograms look like a real system.
package latency

import (
	"context"
	"math/rand/v2"
	"time"
)

// Profile is a fixed base delay plus an exponentially distributed tail with
// mean Jitter, capped at Base + 10*Jitter. The zero value does not sleep.
type Profile struct {
	Base   time.Duration
	Jitter time.Duration
}

// Duration draws one sample from the profile.
func (p Profile) Duration() time.Duration {
	if p.Jitter <= 0 {
		return max(p.Base, 0)
	}
	tail := time.Duration(rand.ExpFloat64() * float64(p.Jitter)) //nolint:gosec // simulated service time, not security sensitive
	return max(p.Base, 0) + min(tail, 10*p.Jitter)
}

// Sleep waits for one sample or until ctx is done, returning ctx.Err() then.
func (p Profile) Sleep(ctx context.Context) error {
	d := p.Duration()
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
