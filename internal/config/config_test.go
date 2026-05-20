package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.TVIP != "" {
		t.Fatalf("expected empty config, got %+v", c)
	}
}

func TestLoadExisting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := os.MkdirAll(filepath.Join(dir, ".config", "cast"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"tv_ip":"172.16.0.5","tv_name":"[TV] Samsung","tv_model":"UN55"}`
	if err := os.WriteFile(filepath.Join(dir, ".config", "cast", "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.TVIP != "172.16.0.5" || c.TVName != "[TV] Samsung" || c.TVModel != "UN55" {
		t.Fatalf("decoded mismatch: %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	in := Config{
		TVIP:             "192.168.1.50",
		TVName:           "[TV] Bedroom",
		TVModel:          "UN55KU6270",
		LastDiscoveredAt: time.Date(2026, 5, 19, 22, 11, 0, 0, time.UTC),
	}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.TVIP != in.TVIP || out.TVName != in.TVName || out.TVModel != in.TVModel {
		t.Fatalf("round trip mismatch: %+v vs %+v", in, out)
	}
	if !out.LastDiscoveredAt.Equal(in.LastDiscoveredAt) {
		t.Fatalf("time mismatch: %v vs %v", in.LastDiscoveredAt, out.LastDiscoveredAt)
	}
}
