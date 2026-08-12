package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

var _ ports.NotificationAgentRepository = (*AgentRepository)(nil)

type AgentRepository struct{ db *gorm.DB }

func NewAgentRepository(db *gorm.DB) *AgentRepository { return &AgentRepository{db: db} }

func (r *AgentRepository) Create(ctx context.Context, ownerID int64, a ports.NotificationAgent) (int64, error) {
	if len(a.ConfigEncrypted) == 0 {
		return 0, fmt.Errorf("create notification agent: config_encrypted required")
	}
	et, err := marshalEventTypes(a.EventTypes)
	if err != nil {
		return 0, err
	}
	m := database.NotificationAgentModel{
		UserID: ownerID, Name: a.Name, Enabled: a.Enabled,
		ConfigEncrypted: a.ConfigEncrypted, EventTypes: et,
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&m).Error; err != nil {
		return 0, fmt.Errorf("create notification agent: %w", err)
	}
	return m.ID, nil
}

func (r *AgentRepository) List(ctx context.Context) ([]ports.NotificationAgent, error) {
	var ms []database.NotificationAgentModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Order("id ASC").Find(&ms).Error; err != nil {
		return nil, fmt.Errorf("list notification agents: %w", err)
	}
	out := make([]ports.NotificationAgent, 0, len(ms))
	for _, m := range ms {
		a, err := toAgent(m)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *AgentRepository) Get(ctx context.Context, id int64) (ports.NotificationAgent, error) {
	var m database.NotificationAgentModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).First(&m, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ports.NotificationAgent{}, ports.ErrNotFound
	}
	if err != nil {
		return ports.NotificationAgent{}, fmt.Errorf("get notification agent: %w", err)
	}
	return toAgent(m)
}

func (r *AgentRepository) Update(ctx context.Context, id int64, name string, enabled bool, eventTypes []string, newConfig []byte) error {
	et, err := marshalEventTypes(eventTypes)
	if err != nil {
		return err
	}
	upd := map[string]any{"name": name, "enabled": enabled, "event_types": et}
	if newConfig != nil { // nil = keep existing ciphertext
		upd["config_encrypted"] = newConfig
	}
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.NotificationAgentModel{}).Where("id = ?", id).Updates(upd)
	if res.Error != nil {
		return fmt.Errorf("update notification agent: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func (r *AgentRepository) Delete(ctx context.Context, id int64) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).Where("id = ?", id).Delete(&database.NotificationAgentModel{})
	if res.Error != nil {
		return fmt.Errorf("delete notification agent: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// ListEnabledForEventAndUser loads userID's enabled agents and filters in Go by
// event_type membership (portable across pg jsonb / sqlite text-json; table is
// tiny so a full scan per due-batch is cheap and avoids dialect-specific JSON
// operators). Ф8-U-5c per-user dispatch — the WHERE user_id predicate prevents
// cross-user leak.
func (r *AgentRepository) ListEnabledForEventAndUser(ctx context.Context, eventType string, userID int64) ([]ports.NotificationAgent, error) {
	var ms []database.NotificationAgentModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("enabled = ? AND user_id = ?", true, userID).Order("id ASC").Find(&ms).Error; err != nil {
		return nil, fmt.Errorf("list enabled notification agents for user: %w", err)
	}
	out := make([]ports.NotificationAgent, 0, len(ms))
	for _, m := range ms {
		a, err := toAgent(m)
		if err != nil {
			return nil, err
		}
		for _, e := range a.EventTypes {
			if e == eventType {
				out = append(out, a)
				break
			}
		}
	}
	return out, nil
}

func marshalEventTypes(ev []string) (datatypes.JSON, error) {
	if ev == nil {
		ev = []string{}
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event_types: %w", err)
	}
	return datatypes.JSON(b), nil
}

func toAgent(m database.NotificationAgentModel) (ports.NotificationAgent, error) {
	var ev []string
	if len(m.EventTypes) > 0 {
		if err := json.Unmarshal(m.EventTypes, &ev); err != nil {
			return ports.NotificationAgent{}, fmt.Errorf("unmarshal event_types: %w", err)
		}
	}
	return ports.NotificationAgent{
		ID: m.ID, Name: m.Name, Enabled: m.Enabled, ConfigEncrypted: m.ConfigEncrypted,
		EventTypes: ev, CreatedAt: m.CreatedAt,
	}, nil
}
