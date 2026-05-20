package cast

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MinUsable is the byte threshold below which we treat a cached MP4 as a
// failed/partial download and re-fetch.
const MinUsable = 1024 * 1024

// SafeFilename sanitizes a title into something safe across filesystems.
// Mirrors the Python implementation: keep [A-Za-z0-9-._], replace the rest
// with underscore, cap at 80 chars.
func SafeFilename(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 80 {
			break
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// FallbackKey returns a short hash used when yt-dlp metadata isn't available.
func FallbackKey(url string) string {
	h := sha1.Sum([]byte(url))
	return hex.EncodeToString(h[:])[:16]
}

// CacheDir resolves the cache directory from CAST_CACHE or the default.
func CacheDir() string {
	if v := os.Getenv("CAST_CACHE"); v != "" {
		return v
	}
	return "/tmp/cast-cache"
}

// EnsureCacheDir creates the cache dir if it doesn't exist.
func EnsureCacheDir() (string, error) {
	d := CacheDir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	return d, nil
}

// CachedPath returns the absolute path for a given cache key.
func CachedPath(key string) string {
	return filepath.Join(CacheDir(), key+".mp4")
}

// HasUsable reports whether the cache key resolves to a file larger than
// MinUsable bytes.
func HasUsable(key string) (string, bool) {
	p := CachedPath(key)
	st, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	if st.Size() <= MinUsable {
		return "", false
	}
	return p, true
}
