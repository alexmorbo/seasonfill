package middleware

import "net"

// IsLocalAddress returns true if ip is contained in any of the
// pre-parsed CIDR allow-list entries. The helper is pure: it takes the
// client IP and the snapshot's already-parsed []*net.IPNet, performs no
// I/O, no allocation beyond the To4() unwrap, and never re-parses CIDRs
// on the hot path.
//
// IPv4-mapped IPv6 unwrap is REQUIRED: on dual-stack hosts (and most
// Linux setups) Gin's c.ClientIP() can return "::ffff:10.0.0.1" for an
// IPv4 client. Without To4(), an IPv4 CIDR like "10.0.0.0/8" would not
// match it and the bypass would silently fail.
//
// Fail-safe semantics:
//   - nil ip                  → false (no bypass)
//   - ip.IsUnspecified()      → false (0.0.0.0 / ::)
//   - empty/nil nets slice    → false (snapshot Defaults() ships non-empty,
//     but an explicit clear via the UI must NOT enable global bypass)
func IsLocalAddress(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil || ip.IsUnspecified() || len(nets) == 0 {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range nets {
		if n == nil {
			continue
		}
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
