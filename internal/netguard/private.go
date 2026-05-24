package netguard

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// ErrBlockedHost is the sentinel wrapped by BlockPrivate when the resolved
// dial address falls inside the RFC1918/loopback/link-local/ULA block set.
// Callers (probe handler) use errors.Is to convert this into a 400.
var ErrBlockedHost = errors.New("blocked host")

// BlockPrivate is a net.Dialer.Control hook. The Go stdlib invokes it AFTER
// DNS resolution with the bound socket and the final dial address (host:port,
// where host is an IP literal). Rejecting here defeats DNS rebinding because
// the IP we inspect is the one the kernel is about to connect to.
//
// Signature matches net.Dialer.Control. The fd argument is unused.
func BlockPrivate(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// Unparseable address — fail closed.
		return fmt.Errorf("%w: %s", ErrBlockedHost, address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Control hook is post-resolution; an unparseable host means the
		// stdlib handed us something unexpected. Fail closed.
		return fmt.Errorf("%w: %s", ErrBlockedHost, address)
	}
	if IsPrivateOrLoopback(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedHost, address)
	}
	_ = network // intentionally unused; signature requirement
	return nil
}

// IsPrivateOrLoopback returns true for IPs in any of:
//   - RFC1918 (10/8, 172.16/12, 192.168/16)
//   - loopback (127/8, ::1)
//   - link-local (169.254/16, fe80::/10)
//   - IPv6 unique local (fc00::/7)
//   - unspecified (0.0.0.0, ::)
//   - multicast
//
// stdlib's ip.IsPrivate covers RFC1918 + ULA but skips link-local and
// loopback, so we layer on top.
func IsPrivateOrLoopback(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsPrivate() {
		return true
	}
	return false
}
