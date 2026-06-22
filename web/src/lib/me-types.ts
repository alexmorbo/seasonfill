// MeResponse is the wire shape of GET /api/v1/me. Hand-rolled because
// openapi-typescript regeneration after N-7a is a separate concern
// (the schema.ts in repo predates story 485). Same pattern lib/auth.ts
// uses for SessionResponse.
//
// Wire fields are snake_case (matches BE dto.MeResponse from story 485
// section 5). Consumers receive snake_case directly — no camelCase
// adapter, unlike Session — because every consumer maps the shape into
// component props that mirror the wire 1:1.
//
// Story 486 (N-7b). N-7c extends with avatar URL helpers + uses the
// same type for the Profile sections.
export interface MeResponse {
  readonly id: number;
  readonly username: string;
  readonly email: string | null;
  readonly role: 'admin' | 'user';
  readonly auth_mode: 'forms' | 'basic' | 'none' | 'oidc';
  readonly avatar_mode: 'auto' | 'monogram' | 'gravatar';
  readonly avatar_resolved_mode: 'gravatar' | 'monogram';
  readonly avatar_hash: string;
  readonly preferred_language: string | null;
  readonly idp_profile_url: string | null;
  readonly oidc_subject: string | null;
  readonly last_login_at: string | null;
}
