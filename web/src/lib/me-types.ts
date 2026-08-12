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
// MePermissions is the nested RBAC-flag object (Ф8-U-6b). Additive: existing
// consumers that ignore `permissions` keep working. For an admin the flags
// reflect the stored columns but role short-circuits enforcement server-side.
export interface MePermissions {
  readonly auto_approve: boolean;
  readonly request: boolean;
  readonly manage_requests: boolean;
  readonly manage_users: boolean;
  readonly request_4k: boolean;
}

export interface MeResponse {
  readonly id: number;
  readonly username: string;
  readonly email: string | null;
  readonly role: 'admin' | 'user';
  // auth_mode is computed per-user by the server: 'jellyfin' for a
  // Jellyfin-provisioned account, 'oidc' when the account has an OIDC subject /
  // no local password, otherwise 'forms'. It is NOT a server-wide setting.
  readonly auth_mode: 'forms' | 'oidc' | 'jellyfin';
  readonly avatar_mode: 'auto' | 'monogram' | 'gravatar';
  readonly avatar_resolved_mode: 'gravatar' | 'monogram';
  readonly avatar_hash: string;
  readonly preferred_language: string | null;
  readonly idp_profile_url: string | null;
  readonly oidc_subject: string | null;
  readonly last_login_at: string | null;
  readonly permissions?: MePermissions;
}
