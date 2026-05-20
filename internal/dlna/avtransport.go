package dlna

import (
	"context"
	"fmt"
	"io"
	"net/http"
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

// GetTransportInfo returns the current TransportState response.
func (c *Client) GetTransportInfo(ctx context.Context) (string, error) {
	return c.soap(ctx, "GetTransportInfo",
		`<u:GetTransportInfo xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">`+
			`<InstanceID>0</InstanceID></u:GetTransportInfo>`)
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
