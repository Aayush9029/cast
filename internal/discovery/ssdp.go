// Package discovery implements SSDP M-SEARCH to find Samsung TVs (and their
// UPnP MediaRenderer service endpoints) on the local network.
//
// Two passes are performed: first a targeted MediaRenderer search, then a
// ssdp:all sweep to catch firmwares that don't reply to the specific ST.
// Each LOCATION URL is fetched, the device descriptor parsed, and only TVs
// matching a Samsung-ish manufacturer/friendlyName are returned.
//
// All network calls honor the caller's context. The package never speaks to
// the AVTransport service itself - callers do that via internal/dlna.
package discovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Device is a flattened view of a Samsung-like UPnP MediaRenderer with the
// service control URLs the rest of the toolkit needs (AVTransport for casting,
// Samsung's MainTVAgent for Smart View pairing).
//
// IP is parsed from LOCATION's host. AVTransportURL and SmartViewURL are
// absolute http:// URLs ready to POST SOAP to.
type Device struct {
	UUID           string
	Name           string
	IP             string
	Model          string
	Manufacturer   string
	FriendlyName   string
	AVTransportURL string
	SmartViewURL   string
	LocationURL    string
}

// Discover runs an SSDP scan and returns deduplicated Samsung-TV-shaped
// devices. A typical good total timeout is 3-4s - the default if you pass 0.
func Discover(ctx context.Context, total time.Duration) ([]Device, error) {
	if total <= 0 {
		total = 4 * time.Second
	}
	scanCtx, cancel := context.WithTimeout(ctx, total)
	defer cancel()

	locations, err := mSearch(scanCtx, []string{
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"ssdp:all",
	})
	if err != nil {
		return nil, err
	}

	seen := map[string]Device{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for loc := range locations {
		loc := loc
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := fetchDevice(scanCtx, loc)
			if err != nil {
				return
			}
			if !looksLikeSamsungTV(d) {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			// Samsung TVs advertise multiple UPnP devices (DIAL, MediaRenderer,
			// MultiScreenService) each with its own UUID at the same IP. Dedupe
			// by IP so the user sees one TV per physical device.
			if _, dup := seen[d.IP]; dup {
				return
			}
			seen[d.IP] = d
		}()
	}
	wg.Wait()

	out := make([]Device, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	return out, nil
}

// mSearch sends one M-SEARCH per ST over a transient UDP socket bound to
// 239.255.255.250:1900, gathers replies for the lifetime of ctx, and returns
// the unique LOCATION headers as a closed channel.
func mSearch(ctx context.Context, sts []string) (<-chan string, error) {
	addr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("ssdp listen: %w", err)
	}

	out := make(chan string, 16)
	deadline, _ := ctx.Deadline()
	if !deadline.IsZero() {
		_ = conn.SetReadDeadline(deadline)
	}

	// Send M-SEARCH for each ST. Multiple repeats catch flaky multicast paths.
	for _, st := range sts {
		body := fmt.Sprintf(
			"M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: %s\r\nUSER-AGENT: tv-cast/1.0 ssdp\r\n\r\n",
			st,
		)
		for i := 0; i < 2; i++ {
			if _, err := conn.WriteToUDP([]byte(body), addr); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("ssdp write: %w", err)
			}
		}
	}

	go func() {
		defer close(out)
		defer conn.Close()
		seen := map[string]struct{}{}
		buf := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			loc := parseLocation(buf[:n])
			if loc == "" {
				continue
			}
			if _, dup := seen[loc]; dup {
				continue
			}
			seen[loc] = struct{}{}
			select {
			case out <- loc:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

// parseLocation extracts the LOCATION: header from an SSDP response (HTTP-like
// over UDP). Returns "" if absent or malformed.
func parseLocation(b []byte) string {
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(b)))
	if _, err := tp.ReadLine(); err != nil {
		return ""
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	return strings.TrimSpace(hdr.Get("Location"))
}

// fetchDevice downloads a UPnP device descriptor and turns it into a Device.
func fetchDevice(ctx context.Context, loc string) (Device, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return Device{}, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Device{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return Device{}, err
	}
	return parseDescriptor(loc, body)
}

// parseDescriptor is split out for unit-test friendliness. The XML schema below
// captures just the fields we care about; the real descriptor is much larger.
func parseDescriptor(loc string, body []byte) (Device, error) {
	var raw struct {
		XMLName xml.Name `xml:"root"`
		Device  struct {
			DeviceType   string `xml:"deviceType"`
			FriendlyName string `xml:"friendlyName"`
			Manufacturer string `xml:"manufacturer"`
			ModelName    string `xml:"modelName"`
			UDN          string `xml:"UDN"`
			ServiceList  struct {
				Service []struct {
					ServiceType string `xml:"serviceType"`
					ControlURL  string `xml:"controlURL"`
				} `xml:"service"`
			} `xml:"serviceList"`
		} `xml:"device"`
	}
	if err := xml.Unmarshal(body, &raw); err != nil {
		return Device{}, fmt.Errorf("parse descriptor: %w", err)
	}

	d := Device{
		UUID:         strings.TrimPrefix(raw.Device.UDN, "uuid:"),
		Name:         raw.Device.FriendlyName,
		Model:        raw.Device.ModelName,
		Manufacturer: raw.Device.Manufacturer,
		FriendlyName: raw.Device.FriendlyName,
		LocationURL:  loc,
	}

	locURL, err := url.Parse(loc)
	if err == nil {
		host := locURL.Hostname()
		d.IP = host
		for _, svc := range raw.Device.ServiceList.Service {
			abs := resolveServiceURL(locURL, svc.ControlURL)
			if abs == "" {
				continue
			}
			st := svc.ServiceType
			switch {
			case strings.Contains(st, "AVTransport"):
				d.AVTransportURL = abs
			case strings.Contains(st, "MainTVAgent"):
				d.SmartViewURL = abs
			}
		}
	}

	return d, nil
}

func resolveServiceURL(base *url.URL, controlPath string) string {
	if controlPath == "" {
		return ""
	}
	ref, err := url.Parse(controlPath)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

// looksLikeSamsungTV filters descriptors we care about. Manufacturer is the
// most reliable signal; friendlyName "[TV] ..." is the user-facing convention
// Samsung sets by default.
func looksLikeSamsungTV(d Device) bool {
	if strings.Contains(strings.ToLower(d.Manufacturer), "samsung") {
		return true
	}
	if strings.HasPrefix(strings.ToUpper(d.FriendlyName), "[TV]") {
		return true
	}
	if strings.Contains(strings.ToLower(d.FriendlyName), "samsung") {
		return true
	}
	return false
}
