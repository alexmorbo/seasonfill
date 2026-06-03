package middleware

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	require.NoError(t, err)
	return n
}

func TestIsLocalAddress_Matrix(t *testing.T) {
	t.Parallel()
	defaults := []*net.IPNet{
		mustCIDR(t, "127.0.0.0/8"),
		mustCIDR(t, "::1/128"),
		mustCIDR(t, "10.0.0.0/8"),
		mustCIDR(t, "172.16.0.0/12"),
		mustCIDR(t, "192.168.0.0/16"),
		mustCIDR(t, "169.254.0.0/16"),
		mustCIDR(t, "fe80::/10"),
		mustCIDR(t, "fc00::/7"),
	}
	cases := []struct {
		name string
		ip   string
		nets []*net.IPNet
		want bool
	}{
		{"ipv4 in 10/8", "10.5.6.7", defaults, true},
		{"ipv4 in 192.168/16", "192.168.1.1", defaults, true},
		{"ipv4 in 172.16/12", "172.20.0.5", defaults, true},
		{"ipv4 in 169.254/16 link-local", "169.254.10.20", defaults, true},
		{"ipv4 public 8.8.8.8", "8.8.8.8", defaults, false},
		{"ipv4 public 1.1.1.1", "1.1.1.1", defaults, false},
		{"loopback v4", "127.0.0.1", defaults, true},
		{"loopback v6", "::1", defaults, true},
		{"ipv6 link-local fe80", "fe80::1", defaults, true},
		{"ipv6 ULA fc00", "fd00::1", defaults, true},
		{"ipv6 public 2001:db8", "2001:db8::1", defaults, false},
		// CRITICAL: dual-stack hosts can deliver IPv4 clients as
		// IPv4-mapped IPv6. Must match the IPv4 CIDR.
		{"ipv4-mapped ipv6 in 10/8", "::ffff:10.5.6.7", defaults, true},
		{"ipv4-mapped ipv6 in 192.168/16", "::ffff:192.168.1.1", defaults, true},
		{"ipv4-mapped ipv6 public", "::ffff:8.8.8.8", defaults, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "test bug: bad ip %q", tc.ip)
			assert.Equal(t, tc.want, IsLocalAddress(ip, tc.nets))
		})
	}
}

func TestIsLocalAddress_NilIP(t *testing.T) {
	t.Parallel()
	nets := []*net.IPNet{mustCIDR(t, "10.0.0.0/8")}
	assert.False(t, IsLocalAddress(nil, nets))
}

func TestIsLocalAddress_UnspecifiedIP(t *testing.T) {
	t.Parallel()
	nets := []*net.IPNet{mustCIDR(t, "0.0.0.0/0")}
	// Even if a caller put 0.0.0.0/0 in the allow-list, the
	// unspecified IP itself never represents a real client.
	assert.False(t, IsLocalAddress(net.ParseIP("0.0.0.0"), nets))
	assert.False(t, IsLocalAddress(net.ParseIP("::"), nets))
}

func TestIsLocalAddress_EmptyNetworks(t *testing.T) {
	t.Parallel()
	ip := net.ParseIP("10.0.0.1")
	assert.False(t, IsLocalAddress(ip, nil))
	assert.False(t, IsLocalAddress(ip, []*net.IPNet{}))
}

func TestIsLocalAddress_NilEntryInList(t *testing.T) {
	t.Parallel()
	// Defensive: a nil entry slipping in must not panic.
	nets := []*net.IPNet{nil, mustCIDR(t, "10.0.0.0/8")}
	assert.True(t, IsLocalAddress(net.ParseIP("10.0.0.1"), nets))
}
