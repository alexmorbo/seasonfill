package wiring

import (
	"log/slog"

	"gorm.io/gorm"

	notifapp "github.com/alexmorbo/seasonfill/internal/notification/app"
	notifpersistence "github.com/alexmorbo/seasonfill/internal/notification/persistence"
	notifrest "github.com/alexmorbo/seasonfill/internal/notification/rest"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// NotificationBundle groups the notification components built at boot.
type NotificationBundle struct {
	OutboxRepo    ports.OutboxRepository
	AgentRepo     ports.NotificationAgentRepository
	Dispatcher    *notifapp.Dispatcher
	AgentsHandler *notifrest.AgentsHandler
}

// BuildNotification constructs the outbox/agent repos, the shoutrrr notifier
// (keyed by the notification-agent-config AES-GCM domain), the dispatcher, and
// the agents REST handler. masterKey is the same runtime master key used for
// qbit/oidc secrets (PersistenceBundle.MasterKey).
func BuildNotification(db *gorm.DB, masterKey string, logger *slog.Logger) (*NotificationBundle, error) {
	cipher, err := crypto.NewNotificationAgentCipher(masterKey)
	if err != nil {
		return nil, err
	}
	outboxRepo := notifpersistence.NewOutboxRepository(db)
	agentRepo := notifpersistence.NewAgentRepository(db)
	notifier := notifapp.NewShoutrrrNotifier(cipher)
	dispatcher := notifapp.NewDispatcher(notifapp.DispatcherDeps{
		Outbox: outboxRepo, Agents: agentRepo, Notifier: notifier, Logger: logger,
	})
	agentsUC := notifapp.NewAgentsUseCase(agentRepo, cipher, notifier)
	handler := notifrest.NewAgentsHandler(agentsUC, logger)
	return &NotificationBundle{
		OutboxRepo: outboxRepo, AgentRepo: agentRepo,
		Dispatcher: dispatcher, AgentsHandler: handler,
	}, nil
}
