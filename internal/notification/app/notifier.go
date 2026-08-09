package app

import (
	"context"
	"fmt"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// Notifier sends a rendered Message to a single agent's destination.
type Notifier interface {
	// Send decrypts the agent's config, parses the shoutrrr URL, and sends
	// title+body. The raw URL is NEVER logged or returned.
	Send(ctx context.Context, configEncrypted []byte, msg Message) error
}

// ShoutrrrNotifier decrypts the AES-GCM agent config to a shoutrrr URL and
// dispatches via the maintained fork nicholas-fedor/shoutrrr.
type ShoutrrrNotifier struct {
	cipher *crypto.Cipher // notification-agent-config domain (N1.9)
}

func NewShoutrrrNotifier(cipher *crypto.Cipher) *ShoutrrrNotifier {
	return &ShoutrrrNotifier{cipher: cipher}
}

func (n *ShoutrrrNotifier) Send(ctx context.Context, configEncrypted []byte, msg Message) error {
	urlBytes, err := n.cipher.Open(configEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt agent config: %w", err) // no URL in error
	}
	sender, err := shoutrrr.CreateSenderWithOptions(types.SenderOptions{}, string(urlBytes))
	if err != nil {
		// shoutrrr error text may echo the scheme but not tokens; still, wrap
		// generically so a bad URL never leaks the raw string upstream.
		return fmt.Errorf("shoutrrr create sender: invalid agent URL")
	}
	params := &types.Params{}
	params.SetTitle(msg.Title)
	errs := sender.Send(msg.Body, params)
	for _, e := range errs {
		if e != nil {
			return fmt.Errorf("shoutrrr send: %w", e)
		}
	}
	_ = ctx // shoutrrr v0.17 Send is synchronous, no ctx param; kept for iface symmetry
	return nil
}
