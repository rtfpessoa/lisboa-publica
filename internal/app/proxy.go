package app

import (
	"net"
	"net/http"
	"net/netip"
)

// clientIP accepts the header Caddy overwrites only from configured proxy networks.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	for _, prefix := range s.trustedProxies {
		if !prefix.Contains(peer.Unmap()) {
			continue
		}
		forwarded, err := netip.ParseAddr(r.Header.Get("X-Lisboa-Client-IP"))
		if err == nil {
			return forwarded.Unmap().String()
		}
	}
	return peer.Unmap().String()
}
