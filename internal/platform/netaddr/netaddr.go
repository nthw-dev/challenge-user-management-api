// Package netaddr turns the address a server listens on into an address a caller can actually dial.
//
// A value like ":8080" or "0.0.0.0:8080" is fine to bind, but you cannot dial it or click it from a terminal.
// This is the only place that knows how to fill in the host, so the HTTP and gRPC sides never convert it differently.
package netaddr

import "net"

// Dialable returns a dialable host:port. If the port cannot be split out, it returns the value unchanged and leaves the decision to the caller.
func Dialable(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

// LocalURL returns a URL that can be clicked straight from a terminal; used when logging which page is served where.
func LocalURL(addr string) string { return "http://" + Dialable(addr) }
