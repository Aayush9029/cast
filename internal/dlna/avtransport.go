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

// Client talks to the Samsung AVTransport1 service over SOAP.
type Client struct {
	endpoint string
	http     *http.Client
}

// New builds a client targeting http://<ip>:9197/upnp/control/AVTransport1.
func New(ip string) *Client {
	return &Client{
		endpoint: fmt.Sprintf("http://%s:9197/upnp/control/AVTransport1", ip),
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) soap(ctx context.Context, action, body string) (string, error) {
	envelope := `<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body>` + body + `</s:Body></s:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(envelope))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", fmt.Sprintf(`"urn:schemas-upnp-org:service:AVTransport:1#%s"`, action))

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

// Stop halts playback on the current instance.
func (c *Client) Stop(ctx context.Context) (string, error) {
	return c.soap(ctx, "Stop",
		`<u:Stop xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+
			`<InstanceID>0</InstanceID></u:Stop>`)
}

// Play starts playback at Speed=1.
func (c *Client) Play(ctx context.Context) (string, error) {
	return c.soap(ctx, "Play",
		`<u:Play xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+
			`<InstanceID>0</InstanceID><Speed>1</Speed></u:Play>`)
}

// Pause pauses playback on the current instance.
func (c *Client) Pause(ctx context.Context) (string, error) {
	return c.soap(ctx, "Pause",
		`<u:Pause xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+
			`<InstanceID>0</InstanceID></u:Pause>`)
}

// GetTransportInfo returns the current TransportState response.
func (c *Client) GetTransportInfo(ctx context.Context) (string, error) {
	return c.soap(ctx, "GetTransportInfo",
		`<u:GetTransportInfo xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+
			`<InstanceID>0</InstanceID></u:GetTransportInfo>`)
}

// GetPositionInfo returns current playback position metadata.
func (c *Client) GetPositionInfo(ctx context.Context) (string, error) {
	return c.soap(ctx, "GetPositionInfo",
		`<u:GetPositionInfo xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+
			`<InstanceID>0</InstanceID></u:GetPositionInfo>`)
}

// Seek moves playback to an absolute timestamp.
func (c *Client) Seek(ctx context.Context, target string) (string, error) {
	return c.soap(ctx, "Seek",
		`<u:Seek xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+
			`<InstanceID>0</InstanceID><Unit>ABS_TIME</Unit>`+
			`<Target>`+xmlEscape(target)+`</Target></u:Seek>`)
}

// SetAVTransportURI configures the URL and DIDL-Lite metadata. Must be called
// before Play.
func (c *Client) SetAVTransportURI(ctx context.Context, streamURL, title string, size int64) (string, error) {
	didl := BuildDIDL(streamURL, title, size)
	body := `<u:SetAVTransportURI xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
		`<InstanceID>0</InstanceID>` +
		`<CurrentURI>` + xmlEscape(streamURL) + `</CurrentURI>` +
		`<CurrentURIMetaData>` + xmlEscape(didl) + `</CurrentURIMetaData>` +
		`</u:SetAVTransportURI>`
	return c.soap(ctx, "SetAVTransportURI", body)
}

// SeekRelative skips relative to the TV's current playback position.
func (c *Client) SeekRelative(ctx context.Context, delta time.Duration) (string, error) {
	resp, err := c.GetPositionInfo(ctx)
	if err != nil {
		return "", err
	}
	current, err := parseDLNATime(CurrentRelTime(resp))
	if err != nil {
		return "", err
	}
	target := current + delta
	if target < 0 {
		target = 0
	}
	if duration, err := parseDLNATime(CurrentTrackDuration(resp)); err == nil && duration > 0 && target > duration {
		target = duration
	}
	return c.Seek(ctx, formatDLNATime(target))
}

// Position returns the TV's current relative playback time as a duration.
func (c *Client) Position(ctx context.Context) (time.Duration, error) {
	resp, err := c.GetPositionInfo(ctx)
	if err != nil {
		return 0, err
	}
	return parseDLNATime(CurrentRelTime(resp))
}

// CurrentTransportState extracts the state from a GetTransportInfo response.
func CurrentTransportState(resp string) string {
	return xmlValue(resp, "CurrentTransportState")
}

// CurrentRelTime extracts the current relative playback time.
func CurrentRelTime(resp string) string {
	return xmlValue(resp, "RelTime")
}

// CurrentTrackDuration extracts the track duration.
func CurrentTrackDuration(resp string) string {
	return xmlValue(resp, "TrackDuration")
}

// TogglePlayPause pauses when playing and plays otherwise.
func (c *Client) TogglePlayPause(ctx context.Context) (string, error) {
	resp, err := c.GetTransportInfo(ctx)
	if err != nil {
		return "", err
	}
	if CurrentTransportState(resp) == "PLAYING" {
		return c.Pause(ctx)
	}
	return c.Play(ctx)
}

func xmlValue(resp, name string) string {
	open := "<" + name + ">"
	close := "</" + name + ">"
	if i := strings.Index(resp, open); i >= 0 {
		rest := resp[i+len(open):]
		if j := strings.Index(rest, close); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

func parseDLNATime(value string) (time.Duration, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid DLNA time %q", value)
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid DLNA hours: %w", err)
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid DLNA minutes: %w", err)
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid DLNA seconds: %w", err)
	}
	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second)), nil
}

func formatDLNATime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	hours := total / 3600
	minutes := total % 3600 / 60
	seconds := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}
