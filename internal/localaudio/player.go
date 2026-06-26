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

// ResyncThreshold is the drift past which the player reseeks. Below it, the
// occasional restart click is more disruptive than the gap.
const ResyncThreshold = 300 * time.Millisecond

// Player drives a single ffplay subprocess against one media file.
type Player struct {
	file string

	mu        sync.Mutex
	cmd       *exec.Cmd
	startPos  time.Duration // -ss value of the running process
	startedAt time.Time     // wall clock at process launch
	clock     func() time.Time
}

// Available reports whether ffplay is on PATH.
func Available() bool {
	_, err := exec.LookPath("ffplay")
	return err == nil
}

// New builds a Player for file. It does not start playback.
func New(file string) *Player {
	return &Player{file: file, clock: time.Now}
}

// PlayFrom kills any running process and starts ffplay seeked to pos.
func (p *Player) PlayFrom(pos time.Duration) error {
	if pos < 0 {
		pos = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killLocked()

	secs := pos.Seconds()
	cmd := exec.Command("ffplay",
		"-nodisp", "-autoexit", "-vn",
		"-loglevel", "quiet",
		"-ss", fmt.Sprintf("%.3f", secs),
		"-i", p.file,
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffplay: %w", err)
	}
	p.cmd = cmd
	p.startPos = pos
	p.startedAt = p.clock()
	return nil
}

// Position estimates the running track position from wall-clock elapsed time.
// Returns false when nothing is playing.
func (p *Player) Position() (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		return 0, false
	}
	return p.startPos + p.clock().Sub(p.startedAt), true
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

// Sync chases tvPos+offset: starts the player if idle, or reseeks when drift
// exceeds ResyncThreshold. It returns the measured drift (player − target) and
// whether a (re)seek was issued.
func (p *Player) Sync(tvPos, offset time.Duration) (drift time.Duration, reseeked bool, err error) {
	target := tvPos + offset
	cur, ok := p.Position()
	if !ok {
		return 0, true, p.PlayFrom(target)
	}
	drift = cur - target
	if drift < 0 {
		drift = -drift
	}
	if drift > ResyncThreshold {
		return drift, true, p.PlayFrom(target)
	}
	return drift, false, nil
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
