package webhookinstall

import (
	"testing"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

func TestMatchesWebhookURL(t *testing.T) {
	t.Parallel()
	field := func(v any) []sonarr.NotificationField {
		return []sonarr.NotificationField{{Name: "url", Value: v}}
	}
	tests := []struct {
		name     string
		fields   []sonarr.NotificationField
		instance string
		want     bool
	}{
		{"exact same host", field("https://sf.example/api/v1/webhook/sonarr/alpha"), "alpha", true},
		{"different host still matches path", field("http://old.example:8080/api/v1/webhook/sonarr/alpha"), "alpha", true},
		{"query string ignored", field("https://sf/api/v1/webhook/sonarr/alpha?k=1"), "alpha", true},
		{"trailing slash tolerated", field("https://sf/api/v1/webhook/sonarr/alpha/"), "alpha", true},
		{"different instance never matches", field("https://sf/api/v1/webhook/sonarr/beta"), "alpha", false},
		{"deeper path not matched", field("https://sf/api/v1/webhook/sonarr/alpha/extra"), "alpha", false},
		{"missing url field", []sonarr.NotificationField{{Name: "method", Value: 1}}, "alpha", false},
		{"non-string value", field(42), "alpha", false},
		{"malformed URL falls back to substring", field("://broken/api/v1/webhook/sonarr/alpha"), "alpha", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchesWebhookURL(tt.fields, tt.instance); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestCanonicalPath(t *testing.T) {
	t.Parallel()
	if got := CanonicalPath("alpha"); got != "/api/v1/webhook/sonarr/alpha" {
		t.Fatalf("unexpected canonical path: %s", got)
	}
}
