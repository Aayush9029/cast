package dlna

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// ProtocolInfo omits DLNA.ORG_PN - letting the TV auto-detect avoids profile
// mismatches on arbitrary H.264/AAC MP4s that don't match a named profile.
const ProtocolInfo = "http-get:*:video/mp4:DLNA.ORG_OP=01;DLNA.ORG_CI=0"

// BuildDIDL produces a DIDL-Lite metadata blob for SetAVTransportURI.
// The output is a single-line XML string ready to be SOAP-escaped into the
// <CurrentURIMetaData> element.
func BuildDIDL(streamURL, title string, size int64) string {
	var b strings.Builder
	b.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:sec="http://www.sec.co.kr/">`)
	b.WriteString(`<item id="1" parentID="0" restricted="1">`)
	b.WriteString("<dc:title>")
	b.WriteString(xmlEscape(title))
	b.WriteString("</dc:title>")
	b.WriteString("<upnp:class>object.item.videoItem.movie</upnp:class>")
	fmt.Fprintf(&b, `<res size="%d" protocolInfo="%s">`, size, ProtocolInfo)
	b.WriteString(xmlEscape(streamURL))
	b.WriteString("</res></item></DIDL-Lite>")
	return b.String()
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
