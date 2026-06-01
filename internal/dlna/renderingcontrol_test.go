package dlna

import (
	"testing"
	"time"
)

func TestCurrentVolume(t *testing.T) {
	got, err := CurrentVolume(`<u:GetVolumeResponse><CurrentVolume>37</CurrentVolume></u:GetVolumeResponse>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != 37 {
		t.Fatalf("volume = %d, want 37", got)
	}
}

func TestClampVolume(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{-1, 0},
		{42, 42},
		{101, 100},
	}
	for _, tt := range tests {
		if got := clampVolume(tt.in); got != tt.want {
			t.Fatalf("clampVolume(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestCurrentTransportState(t *testing.T) {
	got := CurrentTransportState(`<CurrentTransportState>PAUSED_PLAYBACK</CurrentTransportState>`)
	if got != "PAUSED_PLAYBACK" {
		t.Fatalf("state = %q", got)
	}
}

func TestCurrentPositionValues(t *testing.T) {
	resp := `<TrackDuration>01:02:03</TrackDuration><RelTime>00:01:15</RelTime>`
	if got := CurrentTrackDuration(resp); got != "01:02:03" {
		t.Fatalf("duration = %q", got)
	}
	if got := CurrentRelTime(resp); got != "00:01:15" {
		t.Fatalf("rel time = %q", got)
	}
}

func TestDLNATimeRoundTrip(t *testing.T) {
	got, err := parseDLNATime("01:02:03.5")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Hour + 2*time.Minute + 3500*time.Millisecond
	if got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
	if formatted := formatDLNATime(got); formatted != "01:02:04" {
		t.Fatalf("formatted = %q", formatted)
	}
}
