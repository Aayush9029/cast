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
	"strings"
	"time"

	"github.com/Aayush9029/cast/internal/config"
	"github.com/Aayush9029/cast/internal/discovery"
)

// TV is the resolved target with whatever metadata we could gather.
type TV struct {
	IP    string
	Name  string
	Model string

	// Source describes how the IP was resolved, useful for header banners.
	// One of: "flag", "config", "discovery".
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

// Resolve walks the precedence chain. flagIP wins when non-empty. The context
// only bounds SSDP - config/flag paths return immediately.
func Resolve(ctx context.Context, flagIP string) (TV, error) {
	if flagIP != "" {
		return TV{IP: flagIP, Source: "flag"}, nil
	}
	if c, err := config.Load(); err == nil && c.TVIP != "" {
		return TV{
			IP:     c.TVIP,
			Name:   c.TVName,
			Model:  c.TVModel,
			Source: "config",
		}, nil
	}

	scanCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	devs, err := discovery.Discover(scanCtx, 4*time.Second)
	if err != nil {
		return TV{}, fmt.Errorf("SSDP discovery failed: %w", err)
	}
	switch len(devs) {
	case 0:
		return TV{}, NotFoundError{}
	case 1:
		d := devs[0]
		return TV{
			IP:     d.IP,
			Name:   d.FriendlyName,
			Model:  d.Model,
			Source: "discovery",
		}, nil
	default:
		return TV{}, MultipleError{Devices: devs}
	}
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
