package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// AgentView is the SAFE, masked projection returned by the API. It NEVER
// carries the shoutrrr URL — only whether a config is set + a scheme hint.
type AgentView struct {
	ID         int64
	Name       string
	Enabled    bool
	EventTypes []string
	Configured bool
	Scheme     string // e.g. "telegram", "discord" — parsed from the URL scheme only
}

// DefaultEventTypes is the ADR-0016 D3 default subscription set (everything ON
// except grab.ok).
var DefaultEventTypes = []string{
	"grab.failed", "import.failed", "watchdog.regrab", "inbox.dead_letter",
	"season.premiere", "air_date.announced", "digest.weekly",
	"request.approved", "request.denied",
}

// KnownEventTypes gates client input (unknown types rejected 400).
var KnownEventTypes = map[string]struct{}{
	"grab.failed": {}, "import.failed": {}, "grab.ok": {},
	"watchdog.regrab": {}, "inbox.dead_letter": {},
	"season.premiere": {}, "air_date.announced": {}, "digest.weekly": {},
	"request.approved": {}, "request.denied": {},
}

type AgentsUseCase struct {
	repo     ports.NotificationAgentRepository
	cipher   *crypto.Cipher
	notifier Notifier
}

func NewAgentsUseCase(repo ports.NotificationAgentRepository, cipher *crypto.Cipher, notifier Notifier) *AgentsUseCase {
	return &AgentsUseCase{repo: repo, cipher: cipher, notifier: notifier}
}

func (u *AgentsUseCase) List(ctx context.Context) ([]AgentView, error) {
	agents, err := u.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentView, 0, len(agents))
	for _, a := range agents {
		out = append(out, u.toView(a))
	}
	return out, nil
}

func (u *AgentsUseCase) Get(ctx context.Context, id int64) (AgentView, error) {
	a, err := u.repo.Get(ctx, id)
	if err != nil {
		return AgentView{}, err
	}
	return u.toView(a), nil
}

// Create encrypts the shoutrrr URL and persists, stamping ownerID (Ф8-U-5).
// Empty URL is rejected.
func (u *AgentsUseCase) Create(ctx context.Context, ownerID int64, name, url string, enabled bool, eventTypes []string) (int64, error) {
	name = strings.TrimSpace(name)
	url = strings.TrimSpace(url)
	if name == "" {
		return 0, fmt.Errorf("%w: name required", ErrInvalidAgent)
	}
	if url == "" {
		return 0, fmt.Errorf("%w: url required", ErrInvalidAgent)
	}
	et, err := u.validateEventTypes(eventTypes, true)
	if err != nil {
		return 0, err
	}
	enc, err := u.cipher.Seal([]byte(url))
	if err != nil {
		return 0, fmt.Errorf("encrypt agent config: %w", err)
	}
	return u.repo.Create(ctx, ownerID, ports.NotificationAgent{
		Name: name, Enabled: enabled, ConfigEncrypted: enc, EventTypes: et,
	})
}

// Update re-encrypts the URL only when non-empty (empty = keep existing).
func (u *AgentsUseCase) Update(ctx context.Context, id int64, name, url string, enabled bool, eventTypes []string) error {
	name = strings.TrimSpace(name)
	url = strings.TrimSpace(url)
	if name == "" {
		return fmt.Errorf("%w: name required", ErrInvalidAgent)
	}
	et, err := u.validateEventTypes(eventTypes, false)
	if err != nil {
		return err
	}
	var newConfig []byte // nil = keep existing ciphertext
	if url != "" {
		enc, err := u.cipher.Seal([]byte(url))
		if err != nil {
			return fmt.Errorf("encrypt agent config: %w", err)
		}
		newConfig = enc
	}
	return u.repo.Update(ctx, id, name, enabled, et, newConfig)
}

func (u *AgentsUseCase) Delete(ctx context.Context, id int64) error { return u.repo.Delete(ctx, id) }

// Test sends a fixed title+body via the agent's stored config synchronously.
func (u *AgentsUseCase) Test(ctx context.Context, id int64) error {
	a, err := u.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	return u.notifier.Send(ctx, a.ConfigEncrypted, Message{
		Title: "Seasonfill: тестовое уведомление",
		Body:  "Если вы это видите — агент настроен верно.",
	})
}

func (u *AgentsUseCase) validateEventTypes(ev []string, allowDefault bool) ([]string, error) {
	if len(ev) == 0 && allowDefault {
		return append([]string(nil), DefaultEventTypes...), nil
	}
	for _, e := range ev {
		if _, ok := KnownEventTypes[e]; !ok {
			return nil, fmt.Errorf("%w: unknown event_type %q", ErrInvalidAgent, e)
		}
	}
	return ev, nil
}

// toView masks the config: never expose the URL. Scheme is a UX hint only,
// derived from decrypting + splitting on "://" (safe: scheme carries no token).
func (u *AgentsUseCase) toView(a ports.NotificationAgent) AgentView {
	scheme := ""
	if raw, err := u.cipher.Open(a.ConfigEncrypted); err == nil {
		if i := strings.Index(string(raw), "://"); i > 0 {
			scheme = string(raw)[:i]
		}
	}
	return AgentView{
		ID: a.ID, Name: a.Name, Enabled: a.Enabled, EventTypes: a.EventTypes,
		Configured: len(a.ConfigEncrypted) > 0, Scheme: scheme,
	}
}
