package adapters

import (
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestNewWebhookReconcileLookup_SonarrResolves(t *testing.T) {
	t.Parallel()
	reg := catalogrest.InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"son": {
				Config: runtime.InstanceSnapshot{Name: "son", Type: "sonarr"},
				Client: sonarr.New(domain.InstanceName("son"), "http://x", "k", time.Second, nil),
			},
		}
	}}
	lk := NewWebhookReconcileLookup(reg)

	snap, notifier, ok := lk("son")
	if !ok || notifier == nil || snap.Name != "son" {
		t.Fatalf("sonarr instance must resolve to a SonarrNotifier: ok=%v notifier=%v snap=%+v", ok, notifier, snap)
	}
	if _, _, ok := lk("nope"); ok {
		t.Fatalf("unknown name must be ok=false")
	}
}

func TestNewRadarrWebhookReconcileLookup(t *testing.T) {
	t.Parallel()
	holder := NewRadarrInstanceMapHolder(map[string]scan.RadarrInstance{
		"rad": {
			Config: runtime.InstanceSnapshot{Name: "rad", Type: "radarr"},
			Client: radarr.New(domain.InstanceName("rad"), "http://x", "k", time.Second, nil),
		},
		// A radarr row whose client is NOT a *radarr.Client (generated mock)
		// must degrade to ok=false, never panic.
		"badrad": {
			Config: runtime.InstanceSnapshot{Name: "badrad", Type: "radarr"},
			Client: &ports.RadarrClientMock{},
		},
	})
	rlk := NewRadarrWebhookReconcileLookup(holder)

	snap, notifier, ok := rlk("rad")
	if !ok || notifier == nil || snap.Type != "radarr" {
		t.Fatalf("radarr instance must resolve to a RadarrNotifier with snap.Type=radarr: ok=%v snap=%+v", ok, snap)
	}
	if _, _, ok := rlk("badrad"); ok {
		t.Fatalf("non-*radarr.Client must degrade to ok=false")
	}
	if _, _, ok := rlk("nope"); ok {
		t.Fatalf("unknown radarr name must be ok=false")
	}
	if _, _, ok := NewRadarrWebhookReconcileLookup(nil)("rad"); ok {
		t.Fatalf("nil holder must yield ok=false, not panic")
	}
}
