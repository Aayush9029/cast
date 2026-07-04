// Package mirror drives the ScreenCaptureKit helper (`cast-capture`) that powers
// `cast window`: it lists capturable windows and streams a chosen window plus
// its audio as fragmented MP4, which cast serves live to the TV over DLNA.
package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// HelperName is the ScreenCaptureKit companion binary installed alongside cast.
const HelperName = "cast-capture"

// Window is one capturable on-screen window as reported by the helper.
type Window struct {
	ID     uint32 `json:"id"`
	App    string `json:"app"`
	Title  string `json:"title"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Label is a human-friendly one-liner for pick lists.
func (w Window) Label() string {
	t := w.Title
	if t == "" {
		t = "(untitled)"
	}
	return fmt.Sprintf("%s — %s  [%dx%d]", w.App, t, w.Width, w.Height)
}

// HelperPath locates the cast-capture binary: an explicit CAST_CAPTURE override,
// then next to the running cast binary (how Homebrew installs it), then $PATH,
// then the local dev build. Returns a helpful error when it can't be found.
func HelperPath() (string, error) {
	if v := os.Getenv("CAST_CAPTURE"); v != "" {
		return v, nil
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), HelperName)
		if isExec(cand) {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(HelperName); err == nil {
		return p, nil
	}
	for _, cand := range []string{
		"capture/.build/out/Products/Debug/" + HelperName,
		"capture/.build/debug/" + HelperName,
		"capture/.build/release/" + HelperName,
	} {
		if isExec(cand) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%s helper not found — window mirroring needs it installed alongside cast "+
		"(brew reinstall cast) or built with `swift build` in capture/ (then set CAST_CAPTURE)", HelperName)
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// ListWindows runs `cast-capture list` and parses the JSON window array.
func ListWindows(ctx context.Context) ([]Window, error) {
	helper, err := HelperPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, helper, "list")
	var errBuf writeBuf
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		msg := errBuf.String()
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("list windows: %s", msg)
	}
	var windows []Window
	if err := json.Unmarshal(out, &windows); err != nil {
		return nil, fmt.Errorf("parse window list: %w", err)
	}
	return windows, nil
}

// Capture is a running window-capture: the helper process and a pipe carrying
// its fragmented-MP4 output.
type Capture struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

// Start launches `cast-capture stream --window <id> --fps <fps>`. The caller
// serves Capture.Reader() over HTTP and must call Stop when done.
func Start(ctx context.Context, windowID uint32, fps int) (*Capture, error) {
	helper, err := HelperPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, helper, "stream",
		"--window", fmt.Sprintf("%d", windowID), "--fps", fmt.Sprintf("%d", fps))
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start capture: %w", err)
	}
	return &Capture{cmd: cmd, stdout: stdout}, nil
}

// Reader is the live fragmented-MP4 byte stream from the helper.
func (c *Capture) Reader() io.Reader { return c.stdout }

// Stop kills the helper and releases the pipe.
func (c *Capture) Stop() {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.stdout.Close()
	_ = c.cmd.Wait()
}

// LiveHandler streams the capture to the TV. The helper produces a single,
// non-rewindable byte stream, so only the first GET is attached to it; the TV
// is expected to open one long-lived connection. HEAD/probe requests get the
// headers only.
type LiveHandler struct {
	Body io.Reader
	once sync.Once
	used bool
}

func (h *LiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	// No Content-Length: the stream is open-ended, so Go uses chunked encoding.
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	claimed := false
	h.once.Do(func() { h.used = true; claimed = true })
	if !claimed {
		// A second connection can't share the single pipe.
		http.Error(w, "stream already in use", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64*1024)
	for {
		n, err := h.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// writeBuf is a tiny io.Writer that captures the helper's stderr for error
// messages without pulling in bytes.Buffer's full surface.
type writeBuf struct {
	mu  sync.Mutex
	buf []byte
}

func (b *writeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *writeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
