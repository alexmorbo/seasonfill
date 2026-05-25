package netguard

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// ErrBlockedHost is the sentinel wrapped by Guard.Control when the resolved
// dial address falls inside the RFC1918/loopback/link-local/ULA block set.
// Callers (probe handler) use errors.Is to convert this into a 400.
var ErrBlockedHost = errors.New("blocked host")

// Guard is a net.Dialer.Control hook gated by a runtime predicate.
// AllowPrivate is read on every Control call; when it returns true,
// RFC1918/loopback/link-local/ULA destinations are tolerated (homelab
// mode). Nil predicate or false return = fail closed with ErrBlockedHost,
// matching the legacy BlockPrivate contract.
type Guard struct {
	AllowPrivate func() bool
}

// Control matches net.Dialer.Control. Stdlib invokes it AFTER DNS
// resolution with the bound socket and the final dial address — the
// IP we inspect is what the kernel is about to connect to, so DNS
// rebinding is defeated here too.
func (g Guard) Control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlockedHost, address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: %s", ErrBlockedHost, address)
	}
	if IsPrivateOrLoopback(ip) {
		if g.AllowPrivate != nil && g.AllowPrivate() {
			_ = network
			return nil
		}
		return fmt.Errorf("%w: %s", ErrBlockedHost, address)
	}
	_ = network
	return nil
}

// BlockPrivate is the legacy free-function form. Equivalent to
// Guard{}.Control — zero Guard denies every private destination.
//
// Deprecated: prefer constructing a Guard with the runtime predicate.
// Retained so the existing test suite (private_test.go +
// instance_probe_test.go) stays green without churn.
func BlockPrivate(network, address string, raw syscall.RawConn) error {
	return Guard{}.Control(network, address, raw)
}

// IsPrivateOrLoopback returns true for IPs in any of:
//   - RFC1918 (10/8, 172.16/12, 192.168/16)
//   - loopback (127/8, ::1)
//   - link-local (169.254/16, fe80::/10)
//   - IPv6 unique local (fc00::/7)
//   - unspecified (0.0.0.0, ::)
//   - multicast
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
