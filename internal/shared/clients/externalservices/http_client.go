package externalservices

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// defaultTimeout is the *http.Client.Timeout HttpClientFor stamps on
// every returned client. 10s default; downstream clients pick their
// own per-call timeout by overriding .Timeout on the returned value
// before issuing requests. The Test runner clamps to 5s via a
// context.WithTimeout in application/externalservices.Test().
const defaultTimeout = 10 * time.Second

// ErrProxyConfig wraps any proxy-construction failure. The test
// runner uses errors.Is to map it to OutcomeProxyFailed.
var ErrProxyConfig = errors.New("externalservices: proxy config")

// HttpClientFor builds a *http.Client honouring s.ProxyURL. The four
// supported schemes (http, https, socks4, socks5) dispatch into:
//
//   - http / https → stdlib http.Transport{Proxy: http.ProxyURL(u)}.
//     CONNECT tunneling is handled by the transport automatically.
//   - socks4 / socks5 → golang.org/x/net/proxy.FromURL gives us a
//     proxy.Dialer; we wrap it in a DialContext that respects ctx
//     cancellation. proxy.FromURL handles the auth fields embedded in
//     the URL's User.
//
// An empty ProxyURL yields a stock client (cloneDefaultTransport with
// no proxy). Returned errors are wrapped with %w over a sentinel so
// the test runner can classify them as OutcomeProxyFailed.
func HttpClientFor(s Settings) (*http.Client, error) {
	if s.ProxyURL == "" {
		return &http.Client{
			Transport: cloneDefaultTransport(),
			Timeout:   defaultTimeout,
		}, nil
	}
	u, err := url.Parse(s.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("%w: parse proxy url: %w", ErrProxyConfig, err)
	}
	if s.ProxyUsername != "" || s.ProxyPassword != "" {
		u.User = url.UserPassword(s.ProxyUsername, s.ProxyPassword)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		tr := cloneDefaultTransport()
		tr.Proxy = http.ProxyURL(u)
		return &http.Client{Transport: tr, Timeout: defaultTimeout}, nil
	case "socks4", "socks5":
		dialer, err := xproxy.FromURL(u, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("%w: socks dialer: %w", ErrProxyConfig, err)
		}
		tr := cloneDefaultTransport()
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			type ctxDialer interface {
				DialContext(ctx context.Context, network, addr string) (net.Conn, error)
			}
			if cd, ok := dialer.(ctxDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			type result struct {
				c   net.Conn
				err error
			}
			ch := make(chan result, 1)
			go func() {
				c, derr := dialer.Dial(network, addr)
				ch <- result{c, derr}
			}()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case r := <-ch:
				return r.c, r.err
			}
		}
		return &http.Client{Transport: tr, Timeout: defaultTimeout}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported scheme %q", ErrProxyConfig, scheme)
	}
}

// cloneDefaultTransport returns a fresh *http.Transport with the
// stdlib defaults. We never mutate http.DefaultTransport — each
// Settings change gets its own transport so connection pooling is
// isolated per service (avoids one misconfigured proxy poisoning
// pools for the other two).
func cloneDefaultTransport() *http.Transport {
	d := http.DefaultTransport.(*http.Transport)
	return &http.Transport{
		Proxy:                 d.Proxy,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
