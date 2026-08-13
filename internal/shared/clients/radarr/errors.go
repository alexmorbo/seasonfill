package radarr

import (
	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
)

// StatusError is the canonical non-2xx arr error. The concrete type lives in
// arrcore (Ф6-R-2); this alias keeps radarr.StatusError identical to
// arrcore.StatusError so every errors.As(&radarr.StatusError) / errors.Is chain
// resolves the same *arrcore.StatusError the transport surfaces. Mirror of
// sonarr/errors.go:18.
type StatusError = arrcore.StatusError
