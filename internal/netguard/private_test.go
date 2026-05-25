package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPrivateOrLoopback_Positive(t *testing.T) {
	t.Parallel()
	cases := []string{
		"10.0.0.1",
		"10.255.255.255",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.1.1",
		"127.0.0.1",
		"127.255.255.255",
		"169.254.169.254", // AWS IMDS — must be blocked
		"0.0.0.0",
		"::1",
		"fc00::1",
		"fd00::1",
		"fe80::1",
		"::",
		"224.0.0.1", // multicast
	}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(raw)
			require.NotNil(t, ip, "parse %q", raw)
			assert.True(t, IsPrivateOrLoopback(ip), "expected %s to be blocked", raw)
		})
	}
}

func TestIsPrivateOrLoopback_Negative(t *testing.T) {
	t.Parallel()
	cases := []string{
		"1.1.1.1",
		"8.8.8.8",
		"172.15.0.1",  // just outside RFC1918 12-bit range
		"172.32.0.1",  // just outside RFC1918 12-bit range
		"192.169.0.1", // just outside RFC1918
		"2001:db8::1", // documentation prefix, but globally routable as far as classification goes
		"2606:4700::1",
	}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(raw)
			require.NotNil(t, ip, "parse %q", raw)
			assert.False(t, IsPrivateOrLoopback(ip), "expected %s to be allowed", raw)
		})
	}
}

func TestIsPrivateOrLoopback_Nil(t *testing.T) {
	t.Parallel()
	assert.True(t, IsPrivateOrLoopback(nil))
}

func TestBlockPrivate_RejectsPrivate(t *testing.T) {
	t.Parallel()
	err := BlockPrivate("tcp", "10.0.0.1:80", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBlockedHost)
}

func TestBlockPrivate_AllowsPublic(t *testing.T) {
	t.Parallel()
	require.NoError(t, BlockPrivate("tcp", "1.1.1.1:443", nil))
	require.NoError(t, BlockPrivate("tcp", "[2606:4700::1]:443", nil))
}

func TestBlockPrivate_MalformedAddress(t *testing.T) {
	t.Parallel()
	// Missing port.
	err := BlockPrivate("tcp", "10.0.0.1", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBlockedHost)
	// Unparseable host.
	err = BlockPrivate("tcp", "not-an-ip:80", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBlockedHost)
}

// TestBlockPrivate_DNSRebindingDefeated proves the Control hook fires after
// resolution. We build a Dialer with BlockPrivate as Control, then dial a
// hostname that resolves to 127.0.0.1. The dial MUST fail with ErrBlockedHost
// even though the hostname itself is allowed.
func TestBlockPrivate_DNSRebindingDefeated(t *testing.T) {
	t.Parallel()
	// localhost resolves to 127.0.0.1 / ::1 on every supported platform.
	d := &net.Dialer{
		Timeout: 500 * time.Millisecond,
		Control: BlockPrivate,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := d.DialContext(ctx, "tcp", "localhost:1") // port 1 — refused if Control passes
	require.Error(t, err)
	// Unwrap the net.OpError chain.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		assert.ErrorIs(t, opErr.Err, ErrBlockedHost)
		return
	}
	assert.ErrorIs(t, err, ErrBlockedHost)
}

// Sanity check that BlockPrivate is wire-compatible with http.Transport.
func TestBlockPrivate_IntegratesWithHTTPTransport(t *testing.T) {
	t.Parallel()
	tr := &http.Transport{
		DialContext: (&net.Dialer{Control: BlockPrivate, Timeout: 500 * time.Millisecond}).DialContext,
	}
	c := &http.Client{Transport: tr, Timeout: time.Second}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:1/", nil)
	require.NoError(t, err)
	_, err = c.Do(req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBlockedHost)
}
