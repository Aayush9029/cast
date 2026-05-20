package dlna

import (
	"strings"
	"testing"
)

func TestBuildDIDL(t *testing.T) {
	got := BuildDIDL("http://1.2.3.4:8088/video.mp4", "Test & Title", 12345)
	mustContain(t, got, `<dc:title>Test &amp; Title</dc:title>`)
	mustContain(t, got, `protocolInfo="http-get:*:video/mp4:DLNA.ORG_OP=01;DLNA.ORG_CI=0"`)
	mustContain(t, got, `size="12345"`)
	mustContain(t, got, "http://1.2.3.4:8088/video.mp4")
	if strings.Contains(got, "DLNA.ORG_PN") {
		t.Errorf("DLNA.ORG_PN must be omitted to avoid Samsung profile mismatch")
	}
}

func mustContain(t *testing.T, hay, needle string) {
	t.Helper()
	if !strings.Contains(hay, needle) {
		t.Errorf("expected %q in:\n%s", needle, hay)
	}
}
