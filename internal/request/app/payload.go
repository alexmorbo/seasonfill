package app

import (
	"encoding/json"

	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
)

// requestEventPayload builds the request.approved/request.denied outbox
// payload. Routed to the requesting user (user_id) so U-5's per-user dispatch
// can target them; U-2 emits to the global agent set (per-user routing lands
// in U-5). NEVER include secrets.
func requestEventPayload(r reqdomain.Request, status string) []byte {
	b, _ := json.Marshal(map[string]any{
		"request_id": r.ID,
		"user_id":    r.UserID,
		"media_type": r.MediaType,
		"tmdb_id":    r.TMDBID,
		"status":     status,
	})
	return b
}
