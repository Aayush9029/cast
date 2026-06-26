package localaudio

import (
	"os/exec"
	"testing"
	"time"
)

func TestPositionEstimatesFromClock(t *testing.T) {
	now := time.Unix(1000, 0)
	p := New("x.mp4")
	p.clock = func() time.Time { return now }

	p.startPos = 30 * time.Second
	p.startedAt = now
	p.cmd = &exec.Cmd{}

	now = now.Add(2 * time.Second)
	got, ok := p.Position()
	if !ok {
		t.Fatal("expected running")
	}
	if want := 32 * time.Second; got != want {
		t.Fatalf("position = %s, want %s", got, want)
	}
}

func TestPositionNotRunning(t *testing.T) {
	p := New("x.mp4")
	if _, ok := p.Position(); ok {
		t.Fatal("expected not running")
	}
}

func TestSyncNoReseekWithinThreshold(t *testing.T) {
	now := time.Unix(2000, 0)
	p := New("x.mp4")
	p.clock = func() time.Time { return now }
	p.startPos = 10 * time.Second
	p.startedAt = now
	p.cmd = &exec.Cmd{}

	// player at 10s, target = tvPos(10s) + offset(100ms) = 10.1s, drift 100ms.
	drift, reseeked, err := p.Sync(10*time.Second, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if reseeked {
		t.Fatal("should not reseek inside threshold")
	}
	if want := 100 * time.Millisecond; drift != want {
		t.Fatalf("drift = %s, want %s", drift, want)
	}
}
