package cast

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFileHandlerRange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "video.mp4")
	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(p, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(&FileHandler{Path: p})
	defer srv.Close()

	// Full GET → 200, full body, DLNA headers, Accept-Ranges.
	r, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 {
		t.Errorf("full GET: want 200, got %d", r.StatusCode)
	}
	if r.Header.Get("Accept-Ranges") != "bytes" {
		t.Errorf("Accept-Ranges missing")
	}
	if r.Header.Get("transferMode.dlna.org") != "Streaming" {
		t.Errorf("transferMode.dlna.org missing")
	}
	if r.Header.Get("contentFeatures.dlna.org") == "" {
		t.Errorf("contentFeatures.dlna.org missing")
	}
	if r.Header.Get("Content-Type") != "video/mp4" {
		t.Errorf("Content-Type=%q want video/mp4", r.Header.Get("Content-Type"))
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if len(body) != 1024 {
		t.Errorf("body len=%d, want 1024", len(body))
	}

	// Range request → 206 with Content-Range.
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("Range", "bytes=100-199")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if r2.StatusCode != 206 {
		t.Errorf("range GET: want 206, got %d", r2.StatusCode)
	}
	cr := r2.Header.Get("Content-Range")
	if cr != "bytes 100-199/1024" {
		t.Errorf("Content-Range=%q", cr)
	}
	if cl := r2.Header.Get("Content-Length"); cl != strconv.Itoa(100) {
		t.Errorf("Content-Length=%q want 100", cl)
	}
	chunk, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if len(chunk) != 100 {
		t.Errorf("range body len=%d, want 100", len(chunk))
	}
}

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"Hello World":    "Hello_World",
		"S01E02 - Pilot": "S01E02_-_Pilot",
		"abc.def-ghi":    "abc.def-ghi",
	}
	for in, want := range cases {
		got := SafeFilename(in)
		if got != want {
			t.Errorf("SafeFilename(%q)=%q want %q", in, got, want)
		}
	}
}
