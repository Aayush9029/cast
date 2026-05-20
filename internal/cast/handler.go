package cast

import (
	"fmt"
	"net"
	"net/http"
	"os"
)

// DLNAHeaders are the headers required for the Samsung TV's renderer to
// recognize the stream as DLNA-compatible.
var DLNAHeaders = map[string]string{
	"transferMode.dlna.org":    "Streaming",
	"contentFeatures.dlna.org": "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000",
	"Accept-Ranges":            "bytes",
}

// FileHandler serves a single fixed file with proper Range support via
// http.ServeContent. The TV reconnects with Range requests during buffering;
// ServeContent emits 206/Content-Range automatically when Range is present.
type FileHandler struct {
	Path string
}

func (h *FileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(h.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Set DLNA + Content-Type before ServeContent so they apply to both 200
	// and 206 responses. ServeContent will not overwrite Content-Type once set.
	w.Header().Set("Content-Type", "video/mp4")
	for k, v := range DLNAHeaders {
		w.Header().Set(k, v)
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// MyLANIP dials a UDP socket to the TV (no packets sent) and reads back the
// kernel-assigned local address - the IP the TV would reach us at.
func MyLANIP(tvIP string) (string, error) {
	c, err := net.Dial("udp", net.JoinHostPort(tvIP, "1"))
	if err != nil {
		return "", fmt.Errorf("udp dial: %w", err)
	}
	defer c.Close()
	addr := c.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), nil
}
