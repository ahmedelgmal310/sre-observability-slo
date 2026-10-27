package latency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDurationBounds(t *testing.T) {
	p := Profile{Base: 2 * time.Millisecond, Jitter: time.Millisecond}
	for range 1000 {
		d := p.Duration()
		if d < p.Base || d > p.Base+10*p.Jitter {
			t.Fatalf("sample %v outside [%v, %v]", d, p.Base, p.Base+10*p.Jitter)
		}
	}
	if got := (Profile{Base: 5 * time.Millisecond}).Duration(); got != 5*time.Millisecond {
		t.Fatalf("no-jitter duration = %v", got)
	}
	if got := (Profile{Base: -time.Second}).Duration(); got != 0 {
		t.Fatalf("negative base = %v", got)
	}
}

func TestSleep(t *testing.T) {
	if err := (Profile{}).Sleep(context.Background()); err != nil {
		t.Fatalf("zero profile: %v", err)
	}
	start := time.Now()
	if err := (Profile{Base: 10 * time.Millisecond}).Sleep(context.Background()); err != nil || time.Since(start) < 10*time.Millisecond {
		t.Fatalf("sleep returned %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Profile{Base: time.Hour}).Sleep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sleep = %v", err)
	}
}
