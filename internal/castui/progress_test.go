package castui

import "testing"

func TestParseProgressDownloadLines(t *testing.T) {
	cases := []struct {
		line     string
		wantPct  float64
		wantStg  string
		matched  bool
		wantSize string
	}{
		{
			line:     "[download]   0.0% of ~ 1.04GiB at  Unknown B/s ETA Unknown",
			wantPct:  0.0,
			wantStg:  "downloading",
			matched:  true,
			wantSize: "1.04GiB",
		},
		{
			line:     "[download]  12.3% of ~ 1.04GiB at   5.42MiB/s ETA 02:31",
			wantPct:  12.3,
			wantStg:  "downloading",
			matched:  true,
			wantSize: "1.04GiB",
		},
		{
			line:     "[download] 100% of  1.04GiB in 03:21",
			wantPct:  100.0,
			wantStg:  "downloading",
			matched:  true,
			wantSize: "1.04GiB",
		},
		{
			line:    "[info] Writing video subtitles to: foo.en.vtt",
			matched: false,
		},
	}
	for _, tc := range cases {
		got, ok := ParseProgress(tc.line)
		if ok != tc.matched {
			t.Errorf("%q: matched=%v want %v", tc.line, ok, tc.matched)
			continue
		}
		if !tc.matched {
			continue
		}
		if got.Percent != tc.wantPct {
			t.Errorf("%q: pct=%v want %v", tc.line, got.Percent, tc.wantPct)
		}
		if got.Stage != tc.wantStg {
			t.Errorf("%q: stage=%q want %q", tc.line, got.Stage, tc.wantStg)
		}
		if got.Size != tc.wantSize {
			t.Errorf("%q: size=%q want %q", tc.line, got.Size, tc.wantSize)
		}
	}
}

func TestParseProgressDestinationLine(t *testing.T) {
	got, ok := ParseProgress("[download] Destination: /tmp/cast-cache/abc123_Title.mp4")
	if !ok {
		t.Fatal("expected match")
	}
	if got.Stage != "destination" {
		t.Errorf("stage=%q", got.Stage)
	}
	if got.Raw != "/tmp/cast-cache/abc123_Title.mp4" {
		t.Errorf("raw=%q", got.Raw)
	}
}

func TestParseProgressMergerLine(t *testing.T) {
	got, ok := ParseProgress(`[Merger] Merging formats into "/tmp/out.mp4"`)
	if !ok || got.Stage != "merging" {
		t.Errorf("merger detection failed: ok=%v stage=%q", ok, got.Stage)
	}
}
