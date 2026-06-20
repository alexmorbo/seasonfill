package adapters

import (
	"context"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// WatchdogInstanceLister adapts SonarrInstanceRepository to the
// InstanceLister + InstanceIDLookup interfaces the watchdog rollup
// handler depends on. One value satisfies both.
type WatchdogInstanceLister struct {
	repo   *catalogpersistence.SonarrInstanceRepository
	cipher *crypto.Cipher
}

// NewWatchdogInstanceLister wraps the supplied repository + cipher.
func NewWatchdogInstanceLister(repo *catalogpersistence.SonarrInstanceRepository, cipher *crypto.Cipher) WatchdogInstanceLister {
	return WatchdogInstanceLister{repo: repo, cipher: cipher}
}

func (a WatchdogInstanceLister) ListNames(ctx context.Context) ([]string, error) {
	instances, err := a.repo.List(ctx, a.cipher)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(instances))
	for _, inst := range instances {
		out = append(out, inst.Name)
	}
	return out, nil
}

func (a WatchdogInstanceLister) IDByName(ctx context.Context, name string) (uint, bool, error) {
	instances, err := a.repo.List(ctx, a.cipher)
	if err != nil {
		return 0, false, err
	}
	for _, inst := range instances {
		if inst.Name == name {
			return inst.ID, true, nil
		}
	}
	return 0, false, nil
}
