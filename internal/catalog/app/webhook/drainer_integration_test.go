package webhook_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appwebhook "github.com/alexmorbo/seasonfill/internal/catalog/app/webhook"
	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestDrainer_Integration_DrainsRealRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := catalogpersistence.NewWebhookInboxRepository(db)
			ctx := context.Background()

			payload := []byte(`{"eventType":"Grab","downloadId":"HASH","series":{"id":42},"episodes":[{"seasonNumber":3}]}`)
			require.NoError(t, repo.Insert(ctx, ports.WebhookInboxRow{
				InstanceName: "main",
				EventType:    "Grab",
				Payload:      payload,
			}))

			var seen domainwebhook.Event
			var ran bool
			d := appwebhook.NewDrainer(appwebhook.DrainerDeps{
				Inbox:          repo,
				Process:        func(_ context.Context, e domainwebhook.Event) error { seen = e; ran = true; return nil },
				MapEvent:       sonarr.MapWebhookEvent,
				Clock:          clock.Real(),
				PendingCounter: repo,
			})

			// Immediate first pass drains the row; poll for deletion.
			runCtx, cancel := context.WithCancel(ctx)
			go d.RunForever(runCtx)
			require.Eventually(t, func() bool {
				var n int64
				db.Model(&database.WebhookInboxModel{}).Count(&n)
				return n == 0
			}, 3e9, 20e6) // 3s timeout, 20ms poll
			cancel()

			assert.True(t, ran, "Process ran")
			assert.Equal(t, domainwebhook.EventTypeGrabbed, seen.Type)
			assert.Equal(t, 3, seen.SeasonNumber)
		})
	}
}
