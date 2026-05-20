// Package config centralizes TV target resolution and on-disk preferences.
//
// Precedence for the TV IP used by both binaries:
//  1. --tv-ip <addr> flag
//  2. ~/.config/cast/config.json
//  3. SSDP auto-discovery at startup (caller-supplied)
//
// There is intentionally no env var override - discovery is the supported
// auto-config path. Users who want a pinned target either pass the flag or run
// `cast discover` once to populate the config file.
//
// File format (hand-editable, atomic on save):
//
//	{
//	  "tv_ip":   "192.168.0.135",
//	  "tv_name": "[TV] Samsung",
//	  "tv_model": "UN55KU6270",
//	  "last_discovered_at": "2026-05-19T22:11:00Z"
//	}
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Config is the on-disk preference blob. All fields are optional; unset fields
// fall through to the next resolution layer (discovery).
type Config struct {
	TVIP             string    `json:"tv_ip,omitempty"`
	TVName           string    `json:"tv_name,omitempty"`
	TVModel          string    `json:"tv_model,omitempty"`
	LastDiscoveredAt time.Time `json:"last_discovered_at,omitempty"`
}

// Path returns ~/.config/cast/config.json. It mirrors the location used
// by the pairing token so all TV state lives in one directory.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cast", "config.json"), nil
}

// Load reads the config file. A missing file returns an empty Config and no
// error - callers always treat the result as the source of preferences, not a
// presence indicator.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Save writes the config atomically (write-then-rename), creating parent dirs
// as needed with restrictive perms.
func Save(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
