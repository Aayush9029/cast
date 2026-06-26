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
	p.startupLatency = 0

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
	p.startupLatency = 0
	p.startPos = 10 * time.Second
	p.startedAt = now
	p.cmd = &exec.Cmd{}

	// player at 10s, target = tvPos(10s) + offset(100ms) = 10.1s, drift -100ms.
	drift, reseeked, err := p.Sync(10*time.Second, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if reseeked {
		t.Fatal("should not reseek inside threshold")
	}
	if want := -100 * time.Millisecond; drift != want {
		t.Fatalf("drift = %s, want %s", drift, want)
	}
}

func TestSyncDebouncesBeforeReseek(t *testing.T) {
	now := time.Unix(3000, 0)
	p := New("x.mp4")
	p.clock = func() time.Time { return now }
	p.startupLatency = 0
	p.startPos = 0
	p.startedAt = now
	p.cmd = &exec.Cmd{}

	// 2s drift, well over threshold, but must persist ResyncStreak polls.
	for i := 1; i < ResyncStreak; i++ {
		if _, reseeked, _ := p.Sync(2*time.Second, 0); reseeked {
			t.Fatalf("reseeked on poll %d, want debounce until %d", i, ResyncStreak)
		}
	}
	// The streak-th over-threshold poll trips a reseek (PlayFrom fails on the
	// fake file, which is fine — we only assert the decision).
	if _, reseeked, _ := p.Sync(2*time.Second, 0); !reseeked {
		t.Fatalf("expected reseek on poll %d", ResyncStreak)
	}
}
