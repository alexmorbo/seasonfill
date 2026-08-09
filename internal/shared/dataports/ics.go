package dataports

import "context"

// ICSEpochRepository reads and bumps the app_config.ics_epoch revocation
// generation used by the signed ICS calendar-feed token (ADR-0015 §ICS,
// F-14). Deliberately separate from the auth session epoch. GetICSEpoch
// returns 0 when the app_config row is absent (no token could have been
// minted yet). BumpICSEpoch returns the new epoch.
type ICSEpochRepository interface {
	GetICSEpoch(ctx context.Context) (int64, error)
	BumpICSEpoch(ctx context.Context) (int64, error)
}
