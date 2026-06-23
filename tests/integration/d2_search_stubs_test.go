//go:build integration

package integration

import (
	"context"
	"time"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
)

// searchStubListRepo is a no-op DiscoveryListRepo for tests that only
// exercise the /discovery/search route. The Search handler never
// reads through it.
type searchStubListRepo struct{}

func (searchStubListRepo) GetRanked(_ context.Context, _ disco.Kind, _, _ string, _, _ int) (disco.Page, error) {
	return disco.Page{}, nil
}

func (searchStubListRepo) IsStale(_ context.Context, _ disco.Kind, _, _ string, _ time.Duration) (bool, error) {
	return false, nil
}

func (searchStubListRepo) LastRefreshedAt(_ context.Context, _ disco.Kind, _, _ string) (time.Time, error) {
	return time.Time{}, nil
}

func (searchStubListRepo) ReplaceList(_ context.Context, _ disco.Kind, _, _ string, _ []disco.Item) error {
	return nil
}

type searchStubWarming struct{}

func (searchStubWarming) IsWarming() bool { return false }

type searchStubRefresh struct{}

func (searchStubRefresh) RefreshNow(_ context.Context, _ disco.Kind, _, _ string) error { return nil }

// Compile-time guarantees.
var (
	_ discoapp.DiscoveryListRepo = searchStubListRepo{}
	_ discoapp.WarmingProbe      = searchStubWarming{}
	_ discoapp.RefreshOnDemand   = searchStubRefresh{}
)
