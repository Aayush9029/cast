// Package target resolves "which TV are we talking to?" across both binaries.
//
// Order of precedence:
//  1. Explicit --tv-ip flag value (caller passes it in).
//  2. ~/.config/cast/config.json (if tv_ip set).
//  3. SSDP auto-discovery on the LAN. Exactly one match → silent use.
//     Multiple matches → typed MultipleError listing candidates. Zero
//     matches → typed NotFoundError with a "run `cast discover`" hint.
//
// Callers in main packages render errors with .Error() - both error types
// produce a complete, user-ready message (no stack traces, no Go-isms).
package target

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Aayush9029/cast/internal/config"
	"github.com/Aayush9029/cast/internal/discovery"
)

const avTransportPort = "9197"

var (
	loadConfig        = config.Load
	saveConfig        = config.Save
	discoverDevices   = discovery.Discover
	checkAVTransport  = CheckAVTransport
	avTransportDialer = (&net.Dialer{}).DialContext
)

// TV is the resolved target with whatever metadata we could gather.
type TV struct {
	IP    string
	Name  string
	Model string

	// Source describes how the IP was resolved, useful for header banners.
	// One of: "flag", "config", "discovery", "rediscovery".
	Source string
}

// NotFoundError is returned when SSDP yields zero Samsung TVs.
type NotFoundError struct{}

func (NotFoundError) Error() string {
	return "no Samsung TV found on this network.\n" +
		"  - make sure the TV is on and joined to the same Wi-Fi\n" +
		"  - run `cast discover` to scan and save a target\n" +
		"  - or pass --tv-ip <addr>"
}

// MultipleError is returned when discovery finds more than one Samsung TV and
// the caller hasn't pinned one. Listing them inline is more useful than a
// vague "ambiguous" error.
type MultipleError struct {
	Devices []discovery.Device
}

func (e MultipleError) Error() string {
	var b strings.Builder
	b.WriteString("multiple Samsung TVs found:\n")
	for _, d := range e.Devices {
		name := d.FriendlyName
		if name == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(&b, "  - %-24s  %s  %s\n", name, d.Model, d.IP)
	}
	b.WriteString("run `cast discover` to pick one, or pass --tv-ip <addr>")
	return b.String()
}

// Resolve walks the precedence chain. Saved targets are probed before use so
// stale DHCP leases trigger SSDP rediscovery instead of a later SOAP failure.
func Resolve(ctx context.Context, flagIP string) (TV, error) {
	if flagIP != "" {
		if err := checkAVTransport(ctx, flagIP); err != nil {
			return TV{}, fmt.Errorf("TV did not respond at %s:%s: %w", flagIP, avTransportPort, err)
		}
		return TV{IP: flagIP, Source: "flag"}, nil
	}
	c, err := loadConfig()
	if err != nil {
		return TV{}, fmt.Errorf("load config: %w", err)
	}
	if c.TVIP != "" {
		tv := TV{
			IP:     c.TVIP,
			Name:   c.TVName,
			Model:  c.TVModel,
			Source: "config",
		}
		if err := checkAVTransport(ctx, tv.IP); err == nil {
			return tv, nil
		}
		return rediscover(ctx, c)
	}

	return rediscover(ctx, config.Config{})
}

// Rediscover ignores the saved IP, scans again, saves the chosen target, and
// returns it. It is used when a SOAP command fails after initial resolution.
func Rediscover(ctx context.Context, previous TV) (TV, error) {
	return rediscover(ctx, config.Config{
		TVIP:    previous.IP,
		TVName:  previous.Name,
		TVModel: previous.Model,
	})
}

func rediscover(ctx context.Context, previous config.Config) (TV, error) {
	scanCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	devs, err := discoverDevices(scanCtx, 4*time.Second)
	if err != nil {
		return TV{}, fmt.Errorf("SSDP discovery failed: %w", err)
	}
	devs = reachableDevices(ctx, devs)
	switch len(devs) {
	case 0:
		return TV{}, NotFoundError{}
	case 1:
		return saveAndReturn(devs[0], sourceFor(previous))
	default:
		if d, ok := matchPrevious(devs, previous); ok {
			return saveAndReturn(d, "rediscovery")
		}
		return TV{}, MultipleError{Devices: devs}
	}
}

func sourceFor(previous config.Config) string {
	if previous.TVIP == "" && previous.TVName == "" && previous.TVModel == "" {
		return "discovery"
	}
	return "rediscovery"
}

func reachableDevices(ctx context.Context, devs []discovery.Device) []discovery.Device {
	out := make([]discovery.Device, 0, len(devs))
	for _, d := range devs {
		if d.IP == "" {
			continue
		}
		if err := checkAVTransport(ctx, d.IP); err == nil {
			out = append(out, d)
		}
	}
	return out
}

func matchPrevious(devs []discovery.Device, previous config.Config) (discovery.Device, bool) {
	if previous.TVName == "" && previous.TVModel == "" {
		return discovery.Device{}, false
	}
	var matches []discovery.Device
	for _, d := range devs {
		nameMatches := previous.TVName != "" && d.FriendlyName == previous.TVName
		modelMatches := previous.TVModel != "" && d.Model == previous.TVModel
		if nameMatches || modelMatches {
			matches = append(matches, d)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return discovery.Device{}, false
}

func saveAndReturn(d discovery.Device, source string) (TV, error) {
	_ = saveConfig(config.Config{
		TVIP:             d.IP,
		TVName:           d.FriendlyName,
		TVModel:          d.Model,
		LastDiscoveredAt: time.Now().UTC(),
	})
	return TV{IP: d.IP, Name: d.FriendlyName, Model: d.Model, Source: source}, nil
}

// CheckAVTransport verifies that the TV's DLNA control port accepts TCP.
func CheckAVTransport(ctx context.Context, ip string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	conn, err := avTransportDialer(probeCtx, "tcp", net.JoinHostPort(ip, avTransportPort))
	if err != nil {
		return err
	}
	return conn.Close()
}

// Banner formats a resolved TV for header rendering, e.g.
// "[TV] Samsung 7 Series  UN55KU6270  192.168.0.135".
func (t TV) Banner() string {
	parts := []string{}
	if t.Name != "" {
		parts = append(parts, t.Name)
	}
	if t.Model != "" {
		parts = append(parts, t.Model)
	}
	parts = append(parts, t.IP)
	return strings.Join(parts, "  ·  ")
}
