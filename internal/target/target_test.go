package target

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aayush9029/cast/internal/config"
	"github.com/Aayush9029/cast/internal/discovery"
)

func TestResolveUsesReachableConfig(t *testing.T) {
	reset := stubTargetDeps(t)
	defer reset()

	loadConfig = func() (config.Config, error) {
		return config.Config{TVIP: "192.168.0.10", TVName: "[TV] Samsung", TVModel: "UN55"}, nil
	}
	checkAVTransport = func(context.Context, string) error { return nil }

	tv, err := Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if tv.IP != "192.168.0.10" || tv.Source != "config" {
		t.Fatalf("resolved %+v", tv)
	}
}

func TestResolveRediscoveryWhenConfigPortIsDown(t *testing.T) {
	reset := stubTargetDeps(t)
	defer reset()

	var saved config.Config
	loadConfig = func() (config.Config, error) {
		return config.Config{TVIP: "192.168.0.10", TVName: "[TV] Samsung", TVModel: "UN55"}, nil
	}
	checkAVTransport = func(_ context.Context, ip string) error {
		if ip == "192.168.0.10" {
			return errors.New("host is down")
		}
		return nil
	}
	discoverDevices = func(context.Context, time.Duration) ([]discovery.Device, error) {
		return []discovery.Device{{
			IP:           "192.168.0.143",
			FriendlyName: "[TV] Samsung",
			Model:        "UN55",
		}}, nil
	}
	saveConfig = func(c config.Config) error {
		saved = c
		return nil
	}

	tv, err := Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if tv.IP != "192.168.0.143" || tv.Source != "rediscovery" {
		t.Fatalf("resolved %+v", tv)
	}
	if saved.TVIP != "192.168.0.143" {
		t.Fatalf("saved config %+v", saved)
	}
}

func stubTargetDeps(t *testing.T) func() {
	t.Helper()
	origLoad := loadConfig
	origSave := saveConfig
	origDiscover := discoverDevices
	origCheck := checkAVTransport
	return func() {
		loadConfig = origLoad
		saveConfig = origSave
		discoverDevices = origDiscover
		checkAVTransport = origCheck
	}
}
