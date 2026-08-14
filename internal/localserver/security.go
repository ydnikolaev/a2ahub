package localserver

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var (
	// ErrNonLoopback rejects listener and Host values outside loopback.
	ErrNonLoopback = errors.New("localserver: listener must be loopback-only")
	// ErrInvalidHost rejects an HTTP Host header inconsistent with the listener.
	ErrInvalidHost = errors.New("localserver: invalid host")
)

// ValidateListenAddress accepts only a loopback host and numeric TCP port.
func ValidateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: use host:port: %w", ErrNonLoopback, err)
	}
	if port == "" {
		return fmt.Errorf("%w: port is required", ErrNonLoopback)
	}
	numericPort, err := strconv.Atoi(port)
	if err != nil || numericPort < 0 || numericPort > 65535 {
		return fmt.Errorf("%w: invalid port", ErrNonLoopback)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || ip.IsUnspecified() {
		return fmt.Errorf("%w: %q is not a loopback address", ErrNonLoopback, host)
	}
	return nil
}

func validateListener(listener net.Listener) error {
	tcp, ok := listener.Addr().(*net.TCPAddr)
	if !ok || tcp.IP == nil || !tcp.IP.IsLoopback() || tcp.IP.IsUnspecified() {
		return ErrNonLoopback
	}
	return nil
}

func validateRequestHost(value, listenerHost, listenerPort string) error {
	if value == "" {
		return ErrInvalidHost
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		if strings.Contains(value, ":") {
			return ErrInvalidHost
		}
		host = value
		port = ""
	}
	if port != "" && listenerPort != "" && port != listenerPort {
		return ErrInvalidHost
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() || ip.IsUnspecified() {
		return ErrInvalidHost
	}
	configuredIP := net.ParseIP(strings.Trim(listenerHost, "[]"))
	if configuredIP == nil || !ip.Equal(configuredIP) {
		return ErrInvalidHost
	}
	return nil
}

func securityHeaders(header mapHeader) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
}

type mapHeader interface{ Set(string, string) }
