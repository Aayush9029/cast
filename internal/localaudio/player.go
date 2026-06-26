// Package localaudio plays a media file's audio track on the local machine
// (Mac speakers / AirPods) while the video plays on the TV, keeping the two
// roughly in sync by chasing the TV's reported playback position.
//
// Sync strategy: ffplay is started with a fast input seek (-ss) to the TV's
// current position plus a user offset. ffplay then free-runs; a periodic drift
// check compares ffplay's wall-clock-estimated position against a fresh TV
// poll and restarts ffplay when the gap exceeds ResyncThreshold. Precision is
// bounded by DLNA/SOAP latency, so the offset is what dials in lip-sync.
package localaudio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// Sync tuning. The local Mac audio clock is stable, so once aligned the audio
// can free-run; the thing we must NOT do is chase the TV's reported position
// continuously — Samsung's DLNA RelTime lags by seconds and freezes while
// buffering, so reacting to every wobble would knock a hand-tuned offset out
// of place. We therefore only re-seek on a LARGE drift that PERSISTS across
// several polls (a genuine seek/rebuffer, not RelTime noise).
const (
	// ResyncThreshold is the drift past which a re-seek is considered.
	ResyncThreshold = 750 * time.Millisecond
	// ResyncStreak is how many consecutive over-threshold polls must occur
	// before we actually re-seek, debouncing transient TV-clock noise.
	ResyncStreak = 3
	// DefaultStartupLatency pre-rolls the seek to cover the time between
	// launching ffplay and audio actually leaving the speakers, so the first
	// alignment lands right instead of landing ~this much behind.
	DefaultStartupLatency = 250 * time.Millisecond
)

// Player drives a single ffplay subprocess against one media file.
type Player struct {
	file string

	mu             sync.Mutex
	cmd            *exec.Cmd
	startPos       time.Duration // -ss value of the running process
	startedAt      time.Time     // wall clock at process launch
	clock          func() time.Time
	startupLatency time.Duration
	overStreak     int // consecutive over-threshold polls
}

// Available reports whether ffplay is on PATH.
func Available() bool {
	_, err := exec.LookPath("ffplay")
	return err == nil
}

// New builds a Player for file. It does not start playback.
func New(file string) *Player {
	return &Player{file: file, clock: time.Now, startupLatency: DefaultStartupLatency}
}

// PlayFrom kills any running process and starts ffplay aligned to pos. The seek
// is pre-rolled by startupLatency so that, once audio actually leaves the
// speakers, it matches pos rather than landing startupLatency behind it.
func (p *Player) PlayFrom(pos time.Duration) error {
	if pos < 0 {
		pos = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killLocked()

	seekTo := pos + p.startupLatency
	cmd := exec.Command("ffplay",
		"-nodisp", "-autoexit", "-vn",
		"-loglevel", "quiet",
		"-fflags", "nobuffer",
		"-ss", fmt.Sprintf("%.3f", seekTo.Seconds()),
		"-i", p.file,
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffplay: %w", err)
	}
	p.cmd = cmd
	p.startPos = seekTo
	p.startedAt = p.clock()
	p.overStreak = 0
	return nil
}

// Position estimates the audible track position: elapsed wall-clock since
// launch, less the startup latency during which no sound was produced.
// Returns false when nothing is playing.
func (p *Player) Position() (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		return 0, false
	}
	return p.startPos + p.clock().Sub(p.startedAt) - p.startupLatency, true
}

// Running reports whether a process is active.
func (p *Player) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil
}

// Stop kills the process if running.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killLocked()
}

func (p *Player) killLocked() {
	if p.cmd == nil {
		return
	}
	if proc := p.cmd.Process; proc != nil {
		_ = proc.Kill()
		_, _ = proc.Wait()
	}
	p.cmd = nil
}

// Sync aligns local audio to tvPos+offset. It starts the player when idle and
// otherwise lets it free-run, only re-seeking when the drift stays past
// ResyncThreshold for ResyncStreak consecutive polls — that debounce is what
// keeps a hand-tuned offset from being wrecked by the TV's noisy clock.
// It returns the measured drift (player − target) and whether a seek was issued.
func (p *Player) Sync(tvPos, offset time.Duration) (drift time.Duration, reseeked bool, err error) {
	target := tvPos + offset
	cur, ok := p.Position()
	if !ok {
		return 0, true, p.PlayFrom(target)
	}
	drift = cur - target
	mag := drift
	if mag < 0 {
		mag = -mag
	}
	if mag <= ResyncThreshold {
		p.overStreak = 0
		return drift, false, nil
	}
	p.overStreak++
	if p.overStreak < ResyncStreak {
		return drift, false, nil
	}
	return drift, true, p.PlayFrom(target)
}

// ErrUnavailable is returned by Require when ffplay is missing.
var ErrUnavailable = errors.New("ffplay not found on PATH; install ffmpeg (brew install ffmpeg)")

// Require errors if ffplay is unavailable. Lets callers fail fast with a clear
// message before touching the TV.
func Require(_ context.Context) error {
	if !Available() {
		return ErrUnavailable
	}
	return nil
}
