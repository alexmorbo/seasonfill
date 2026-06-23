package dto

import "time"

// ExternalServiceDTO is the masked wire shape returned by GET / PUT.
// Plaintext keys NEVER appear here; api_key_masked is "****" + last4.
type ExternalServiceDTO struct {
	Service          string     `json:"service"            example:"tmdb"`
	Enabled          bool       `json:"enabled"`
	APIKeyMasked     string     `json:"api_key_masked"     example:"****abcd"`
	APIKeyConfigured bool       `json:"api_key_configured"`
	ProxyURLSet      bool       `json:"proxy_url_set"`
	ProxyAuthSet     bool       `json:"proxy_auth_set"`
	ProxyScheme      string     `json:"proxy_scheme,omitempty"  example:"socks5"`
	ProxyHost        string     `json:"proxy_host,omitempty"    example:"proxy.example.com:1080"`
	LastTestAt       *time.Time `json:"last_test_at,omitempty"`
	LastTestOutcome  string     `json:"last_test_outcome,omitempty" example:"ok"`
	LastTestMessage  string     `json:"last_test_message,omitempty"`
	// Story 489 (B-17): runtime validation status. Empty when the
	// service was never validated (live 401 hook never fired AND the
	// operator has not saved a key); "valid" or "invalid_key" otherwise.
	LastValidationAt      *time.Time `json:"last_validation_at,omitempty"`
	LastValidationStatus  string     `json:"last_validation_status,omitempty" enums:"valid,invalid_key"`
	LastValidationMessage string     `json:"last_validation_message,omitempty"`
}

// ExternalServiceListResponse wraps the list endpoint return.
type ExternalServiceListResponse struct {
	Services []ExternalServiceDTO `json:"services"`
}

// ExternalServiceUpsertRequest carries the operator-supplied PUT body.
// Pointer fields implement the PUT semantics: nil = unchanged in JSON
// (omitted by the client), explicit "" = clear, non-empty = set.
type ExternalServiceUpsertRequest struct {
	Enabled       bool    `json:"enabled"`
	APIKey        *string `json:"api_key,omitempty"`
	ProxyURL      *string `json:"proxy_url,omitempty"`
	ProxyUsername *string `json:"proxy_username,omitempty"`
	ProxyPassword *string `json:"proxy_password,omitempty"`
}

// ExternalServiceTestResponse is the result of POST :test.
type ExternalServiceTestResponse struct {
	Outcome   string `json:"outcome"      example:"ok"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}
