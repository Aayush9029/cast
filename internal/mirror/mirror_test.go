package mirror

import (
	"encoding/json"
	"os"
	"testing"
)

func TestWindowLabel(t *testing.T) {
	cases := []struct {
		w    Window
		want string
	}{
		{Window{App: "Zen", Title: "YouTube", Width: 1280, Height: 720}, "Zen — YouTube  [1280x720]"},
		{Window{App: "Terminal", Title: "", Width: 800, Height: 600}, "Terminal — (untitled)  [800x600]"},
	}
	for _, c := range cases {
		if got := c.w.Label(); got != c.want {
			t.Errorf("Label() = %q, want %q", got, c.want)
		}
	}
}

func TestListWindowsParse(t *testing.T) {
	// Mirrors the helper's JSON so a schema drift breaks the test, not a cast.
	const raw = `[{"id":42,"app":"Safari","title":"Hi","width":1920,"height":1080}]`
	var ws []Window
	if err := json.Unmarshal([]byte(raw), &ws); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ws) != 1 || ws[0].ID != 42 || ws[0].App != "Safari" || ws[0].Width != 1920 {
		t.Fatalf("unexpected parse: %+v", ws)
	}
}

func TestHelperPathEnvOverride(t *testing.T) {
	t.Setenv("CAST_CAPTURE", "/custom/cast-capture")
	got, err := HelperPath()
	if err != nil || got != "/custom/cast-capture" {
		t.Fatalf("HelperPath() = %q, %v; want /custom/cast-capture", got, err)
	}
}

func TestHelperPathMissing(t *testing.T) {
	t.Setenv("CAST_CAPTURE", "")
	// Point PATH somewhere empty and run from a temp dir so no dev-build or
	// sibling binary is found, exercising the not-found error path.
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := HelperPath(); err == nil {
		t.Fatal("expected error when helper is absent")
	}
}
