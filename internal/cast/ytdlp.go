package cast

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const defaultCookiesBrowser = "safari"

// CookiesBrowser resolves CAST_COOKIES, defaulting to "safari".
func CookiesBrowser() string {
	if v := os.Getenv("CAST_COOKIES"); v != "" {
		return v
	}
	return defaultCookiesBrowser
}

// Metadata is the bare yt-dlp probe result.
type Metadata struct {
	ID    string
	Title string
}

// Probe runs `yt-dlp --skip-download --print "%(id)s|%(title)s"` to derive a
// stable cache key. Any failure is non-fatal - the caller falls back to a
// URL hash.
func Probe(ctx context.Context, url string) (Metadata, error) {
	cmd := exec.CommandContext(ctx,
		"yt-dlp", "--cookies-from-browser", CookiesBrowser(),
		"--no-playlist", "--skip-download",
		"--print", "%(id)s|%(title)s", url,
	)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return Metadata{}, fmt.Errorf("yt-dlp probe: %w (%s)", err, strings.TrimSpace(errOut.String()))
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) == 0 {
		return Metadata{}, fmt.Errorf("yt-dlp probe: empty output")
	}
	last := lines[len(lines)-1]
	id, title, ok := strings.Cut(last, "|")
	if !ok {
		return Metadata{}, fmt.Errorf("yt-dlp probe: malformed line %q", last)
	}
	return Metadata{ID: id, Title: title}, nil
}

// CacheKey derives a stable cache key from probe metadata; on any error,
// falls back to a SHA1 hash of the URL.
func CacheKey(ctx context.Context, url string) string {
	m, err := Probe(ctx, url)
	if err != nil {
		return FallbackKey(url)
	}
	return m.ID + "_" + SafeFilename(m.Title)
}

// Download invokes yt-dlp and streams its merged stdout+stderr to `progress`
// line-by-line. Mirrors the original flags exactly.
func Download(ctx context.Context, url, outPath string, progress io.Writer) error {
	return DownloadLines(ctx, url, outPath, func(line string) {
		fmt.Fprintln(progress, line)
	})
}

// DownloadLines is the same as Download but delivers each stdout line to a
// callback instead of a Writer. Useful for TUIs that need to parse the
// `[download] ... %` lines into a progress bar.
func DownloadLines(ctx context.Context, url, outPath string, onLine func(string)) error {
	args := []string{
		"--cookies-from-browser", CookiesBrowser(),
		"--no-playlist",
		"-f", "bv*[height<=1080]+ba/b",
		"--merge-output-format", "mp4",
		"--remux-video", "mp4",
		"-N", "8",
		"-o", outPath,
		"--no-mtime",
		"--newline",
		url,
	}
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("yt-dlp start: %w", err)
	}
	scan := bufio.NewScanner(pipe)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		if onLine != nil {
			onLine(scan.Text())
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("yt-dlp: %w", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("yt-dlp produced no file at %s", outPath)
	}
	return nil
}
