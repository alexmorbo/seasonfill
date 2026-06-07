package main

import (
	"context"

	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// watchdogInstanceLister adapts SonarrInstanceRepository to the
// InstanceLister + InstanceIDLookup interfaces the watchdog rollup
// handler depends on. One value satisfies both.
type watchdogInstanceLister struct {
	repo   *repositories.SonarrInstanceRepository
	cipher *crypto.Cipher
}

func (a watchdogInstanceLister) ListNames(ctx context.Context) ([]string, error) {
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

func (a watchdogInstanceLister) IDByName(ctx context.Context, name string) (uint, bool, error) {
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
