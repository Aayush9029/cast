package discovery

import (
	"strings"
	"testing"
)

// Sample shaped to match Samsung's :9197/dmr descriptor. The IP/UDN/model are
// synthetic, but the element nesting (root > device > serviceList > service)
// matches what real Tizen firmwares return.
const sampleSamsungDescriptor = `<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>[TV] Samsung 7 Series (55)</friendlyName>
    <manufacturer>Samsung Electronics</manufacturer>
    <manufacturerURL>http://www.samsung.com/sec</manufacturerURL>
    <modelDescription>Samsung TV DMR</modelDescription>
    <modelName>UN55KU6270</modelName>
    <modelNumber>1.0</modelNumber>
    <UDN>uuid:abcd1234-0000-1000-8000-aabbccddeeff</UDN>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
        <controlURL>/upnp/control/RenderingControl1</controlURL>
        <eventSubURL>/upnp/event/RenderingControl1</eventSubURL>
        <SCPDURL>/RenderingControl1.xml</SCPDURL>
      </service>
      <service>
        <serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId>
        <controlURL>/upnp/control/ConnectionManager1</controlURL>
        <eventSubURL>/upnp/event/ConnectionManager1</eventSubURL>
        <SCPDURL>/ConnectionManager1.xml</SCPDURL>
      </service>
      <service>
        <serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
        <controlURL>/upnp/control/AVTransport1</controlURL>
        <eventSubURL>/upnp/event/AVTransport1</eventSubURL>
        <SCPDURL>/AVTransport1.xml</SCPDURL>
      </service>
      <service>
        <serviceType>urn:samsung.com:service:MainTVAgent2:1</serviceType>
        <serviceId>urn:samsung.com:serviceId:MainTVAgent2</serviceId>
        <controlURL>/PMR/control/MainTVAgent2</controlURL>
        <eventSubURL>/PMR/event/MainTVAgent2</eventSubURL>
        <SCPDURL>/PMR/MainTVAgent2.xml</SCPDURL>
      </service>
    </serviceList>
  </device>
</root>`

const nonSamsungDescriptor = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>Sonos Living Room</friendlyName>
    <manufacturer>Sonos</manufacturer>
    <modelName>PLAY:1</modelName>
    <UDN>uuid:ffff0000-1111-2222-3333-444455556666</UDN>
    <serviceList/>
  </device>
</root>`

func TestParseDescriptorSamsung(t *testing.T) {
	d, err := parseDescriptor("http://192.168.0.135:9197/dmr", []byte(sampleSamsungDescriptor))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.IP != "192.168.0.135" {
		t.Errorf("IP: got %q", d.IP)
	}
	if d.Model != "UN55KU6270" {
		t.Errorf("Model: got %q", d.Model)
	}
	if d.UUID != "abcd1234-0000-1000-8000-aabbccddeeff" {
		t.Errorf("UUID: got %q", d.UUID)
	}
	wantAV := "http://192.168.0.135:9197/upnp/control/AVTransport1"
	if d.AVTransportURL != wantAV {
		t.Errorf("AVTransportURL: got %q, want %q", d.AVTransportURL, wantAV)
	}
	wantSV := "http://192.168.0.135:9197/PMR/control/MainTVAgent2"
	if d.SmartViewURL != wantSV {
		t.Errorf("SmartViewURL: got %q, want %q", d.SmartViewURL, wantSV)
	}
	if !looksLikeSamsungTV(d) {
		t.Errorf("expected Samsung TV match")
	}
}

func TestParseDescriptorNonSamsung(t *testing.T) {
	d, err := parseDescriptor("http://192.168.0.50:1400/xml/device_description.xml", []byte(nonSamsungDescriptor))
	if err != nil {
		t.Fatal(err)
	}
	if looksLikeSamsungTV(d) {
		t.Errorf("Sonos should not match")
	}
}

func TestParseLocationHeader(t *testing.T) {
	resp := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nLOCATION: http://192.168.0.135:9197/dmr\r\nSERVER: SHP, UPnP/1.0, Samsung UPnP SDK/1.0\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nUSN: uuid:abcd::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n"
	got := parseLocation([]byte(resp))
	if got != "http://192.168.0.135:9197/dmr" {
		t.Fatalf("got %q", got)
	}
}

func TestParseLocationMissing(t *testing.T) {
	resp := "HTTP/1.1 200 OK\r\nFOO: bar\r\n\r\n"
	if got := parseLocation([]byte(resp)); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLooksLikeSamsungTVHeuristics(t *testing.T) {
	cases := []struct {
		name string
		dev  Device
		want bool
	}{
		{"manufacturer match", Device{Manufacturer: "Samsung Electronics"}, true},
		{"friendly name TV prefix", Device{FriendlyName: "[TV] Bedroom"}, true},
		{"friendly name samsung substring", Device{FriendlyName: "Samsung Living Room"}, true},
		{"unrelated renderer", Device{Manufacturer: "Roku", FriendlyName: "Roku Player"}, false},
		{"empty", Device{}, false},
	}
	for _, tc := range cases {
		if got := looksLikeSamsungTV(tc.dev); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestResolveServiceURL(t *testing.T) {
	// Indirect via parseDescriptor - exercises url.ResolveReference.
	d, _ := parseDescriptor("http://10.0.0.7:9197/dmr", []byte(sampleSamsungDescriptor))
	if !strings.HasPrefix(d.AVTransportURL, "http://10.0.0.7:9197/") {
		t.Fatalf("relative path resolution wrong: %q", d.AVTransportURL)
	}
}
