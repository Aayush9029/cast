package castui

import (
	"regexp"
	"strconv"
	"strings"
)

// ProgressLine is a parsed yt-dlp `--newline` download line. Any field can be
// zero if the line didn't include it (e.g. early "Destination:" lines).
type ProgressLine struct {
	Percent float64 // 0..100
	Size    string  // human-readable, e.g. "1.23GiB"
	Speed   string  // e.g. "5.42MiB/s"
	ETA     string  // e.g. "00:42"
	Stage   string  // free-form, used for non-percent updates ("Destination", "Merging", etc.)
	Raw     string  // original line, for the verbose log
}

// `--newline` lines look like one of:
//   [download]   0.0% of ~ 1.04GiB at  Unknown B/s ETA Unknown
//   [download]  12.3% of ~ 1.04GiB at   5.42MiB/s ETA 02:31
//   [download] 100% of ~ 1.04GiB in 03:21
//   [download] Destination: /tmp/foo.mp4
//   [Merger] Merging formats into "/tmp/foo.mp4"
//
// We tolerate variable whitespace and the "of ~" approximation marker.

var (
	progressRe = regexp.MustCompile(
		`(?i)\[download\]\s+([\d.]+)%\s+of\s+~?\s*([0-9.]+\s*[KMGT]?i?B)(?:\s+at\s+([^\s]+\s*[KMGT]?i?B/s|Unknown\s*B/s|Unknown))?(?:\s+ETA\s+(\S+))?`,
	)
	destRe = regexp.MustCompile(`(?i)\[(?:download|info)\]\s+Destination:\s+(.+)`)
)

// ParseProgress turns a single yt-dlp stdout line into a ProgressLine. Returns
// (line, false) for lines that aren't download-related (we still want to keep
// them for the verbose log).
func ParseProgress(line string) (ProgressLine, bool) {
	line = strings.TrimRight(line, "\r\n")
	if m := progressRe.FindStringSubmatch(line); m != nil {
		pct, _ := strconv.ParseFloat(m[1], 64)
		return ProgressLine{
			Percent: pct,
			Size:    strings.TrimSpace(m[2]),
			Speed:   strings.TrimSpace(m[3]),
			ETA:     strings.TrimSpace(m[4]),
			Stage:   "downloading",
			Raw:     line,
		}, true
	}
	if m := destRe.FindStringSubmatch(line); m != nil {
		return ProgressLine{Stage: "destination", Raw: strings.TrimSpace(m[1])}, true
	}
	if strings.Contains(line, "[Merger]") || strings.Contains(strings.ToLower(line), "merging") {
		return ProgressLine{Stage: "merging", Raw: line}, true
	}
	if strings.HasPrefix(line, "[ExtractAudio]") || strings.HasPrefix(line, "[VideoRemuxer]") {
		return ProgressLine{Stage: "remuxing", Raw: line}, true
	}
	return ProgressLine{Raw: line}, false
}
