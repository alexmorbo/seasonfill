package webhookinstall

import (
	"testing"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
)

func TestMatchesWebhookURLRadarr(t *testing.T) {
	t.Parallel()
	field := func(v any) []radarr.NotificationField {
		return []radarr.NotificationField{{Name: "url", Value: v}}
	}
	tests := []struct {
		name     string
		fields   []radarr.NotificationField
		instance string
		want     bool
	}{
		{"exact same host", field("https://sf.example/api/v1/webhook/radarr/alpha"), "alpha", true},
		{"different host still matches path", field("http://old.example:8080/api/v1/webhook/radarr/alpha"), "alpha", true},
		{"query string ignored", field("https://sf/api/v1/webhook/radarr/alpha?k=1"), "alpha", true},
		{"trailing slash tolerated", field("https://sf/api/v1/webhook/radarr/alpha/"), "alpha", true},
		{"different instance never matches", field("https://sf/api/v1/webhook/radarr/beta"), "alpha", false},
		{"deeper path not matched", field("https://sf/api/v1/webhook/radarr/alpha/extra"), "alpha", false},
		{"sonarr path never matches radarr", field("https://sf/api/v1/webhook/sonarr/alpha"), "alpha", false},
		{"missing url field", []radarr.NotificationField{{Name: "method", Value: 1}}, "alpha", false},
		{"non-string value", field(42), "alpha", false},
		{"malformed URL falls back to substring", field("://broken/api/v1/webhook/radarr/alpha"), "alpha", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchesWebhookURLRadarr(tt.fields, tt.instance); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestCanonicalPathRadarr(t *testing.T) {
	t.Parallel()
	if got := CanonicalPathRadarr("alpha"); got != "/api/v1/webhook/radarr/alpha" {
		t.Fatalf("unexpected radarr canonical path: %s", got)
	}
}
