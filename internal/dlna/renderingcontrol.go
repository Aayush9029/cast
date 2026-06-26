package dlna

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RenderingControl talks to the UPnP service that owns TV volume.
type RenderingControl struct {
	endpoint string
	http     *http.Client
}

// NewRenderingControl targets http://<ip>:9197/upnp/control/RenderingControl1.
func NewRenderingControl(ip string) *RenderingControl {
	return &RenderingControl{
		endpoint: fmt.Sprintf("http://%s:9197/upnp/control/RenderingControl1", ip),
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *RenderingControl) soap(ctx context.Context, action, body string) (string, error) {
	envelope := `<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body>` + body + `</s:Body></s:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(envelope))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", fmt.Sprintf(`"urn:schemas-upnp-org:service:RenderingControl:1#%s"`, action))

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("soap %s: %w", action, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("soap %s read: %w", action, err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("soap %s: HTTP %d: %s", action, resp.StatusCode, string(b))
	}
	return string(b), nil
}

// GetVolume returns the TV's master volume, usually 0-100.
func (c *RenderingControl) GetVolume(ctx context.Context) (int, error) {
	resp, err := c.soap(ctx, "GetVolume",
		`<u:GetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">`+
			`<InstanceID>0</InstanceID><Channel>Master</Channel></u:GetVolume>`)
	if err != nil {
		return 0, err
	}
	return CurrentVolume(resp)
}

// SetVolume sets the TV's master volume, clamped to the usual 0-100 range.
func (c *RenderingControl) SetVolume(ctx context.Context, volume int) error {
	volume = clampVolume(volume)
	_, err := c.soap(ctx, "SetVolume",
		`<u:SetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">`+
			`<InstanceID>0</InstanceID><Channel>Master</Channel>`+
			`<DesiredVolume>`+strconv.Itoa(volume)+`</DesiredVolume></u:SetVolume>`)
	return err
}

// AdjustVolume reads the current volume, applies delta, sets it, and returns it.
func (c *RenderingControl) AdjustVolume(ctx context.Context, delta int) (int, error) {
	volume, err := c.GetVolume(ctx)
	if err != nil {
		return 0, err
	}
	volume = clampVolume(volume + delta)
	if err := c.SetVolume(ctx, volume); err != nil {
		return 0, err
	}
	return volume, nil
}

// SetMute mutes or unmutes the TV's master channel.
func (c *RenderingControl) SetMute(ctx context.Context, mute bool) error {
	desired := "0"
	if mute {
		desired = "1"
	}
	_, err := c.soap(ctx, "SetMute",
		`<u:SetMute xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">`+
			`<InstanceID>0</InstanceID><Channel>Master</Channel>`+
			`<DesiredMute>`+desired+`</DesiredMute></u:SetMute>`)
	return err
}

// CurrentVolume extracts CurrentVolume from a GetVolume response.
func CurrentVolume(resp string) (int, error) {
	const open = "<CurrentVolume>"
	const close = "</CurrentVolume>"
	if i := strings.Index(resp, open); i >= 0 {
		rest := resp[i+len(open):]
		if j := strings.Index(rest, close); j >= 0 {
			volume, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
			if err != nil {
				return 0, fmt.Errorf("parse volume: %w", err)
			}
			return volume, nil
		}
	}
	return 0, fmt.Errorf("volume not found in response")
}

func clampVolume(volume int) int {
	if volume < 0 {
		return 0
	}
	if volume > 100 {
		return 100
	}
	return volume
}
