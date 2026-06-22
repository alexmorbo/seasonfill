import { useState } from 'react';
import { cn } from '@/lib/utils';
import { MonogramFallback } from './MonogramFallback';

// Avatar resolved mode (after BE auto-resolution).
//
// Story 487 (N-7c). The BE returns `avatar_resolved_mode` already
// computed from `avatar_mode + email_present` (N-7a Decision §2),
// so this component does not re-derive — it just consumes.
export type AvatarResolvedMode = 'gravatar' | 'monogram';

export interface AvatarProps {
  readonly avatar_resolved_mode: AvatarResolvedMode;
  readonly avatar_hash: string;
  readonly username: string;
  /** Pixel size for the square avatar container. Defaults to 64. */
  readonly size?: number;
  /** Optional className passed to the outer wrapper. */
  readonly className?: string;
  readonly 'data-testid'?: string;
}

// Gravatar image URL. d=404 means "return HTTP 404 if no gravatar
// registered for this hash" — that triggers the <img onError> branch
// which silently falls back to MonogramFallback. We request 2× the
// display size for retina screens.
function gravatarURL(hash: string, size: number): string {
  return `https://gravatar.com/avatar/${hash}?s=${size * 2}&d=404`;
}

// Avatar renders either a Gravatar img (with silent fallback to
// monogram on 404) or the engraved monogram directly. The container
// is a round-clip box sized exactly `size × size`. MonogramFallback
// is `absolute inset-0` so it fills the round-clip when used.
//
// Why a single component vs split: the BE's `avatar_resolved_mode`
// already encodes the "auto resolution" rule, so this component is
// the only place in the FE that knows the mode → render mapping.
// Reused by ProfileSection (size=64) and AppearanceSection preview
// (size=96).
export function Avatar({
  avatar_resolved_mode,
  avatar_hash,
  username,
  size = 64,
  className,
  ...rest
}: AvatarProps) {
  const [imgFailed, setImgFailed] = useState(false);

  const wantsGravatar =
    avatar_resolved_mode === 'gravatar' && avatar_hash !== '' && !imgFailed;

  return (
    <div
      data-testid={rest['data-testid'] ?? 'avatar'}
      data-resolved-mode={imgFailed ? 'monogram' : avatar_resolved_mode}
      className={cn(
        'relative overflow-hidden rounded-full bg-surface',
        className,
      )}
      style={{ width: size, height: size }}
    >
      {wantsGravatar ? (
        <img
          src={gravatarURL(avatar_hash, size)}
          alt={`Avatar of ${username}`}
          width={size}
          height={size}
          // Silent fallback: 404 → switch to monogram on next render.
          // Gravatar returns 404 (per d=404 query param) when there is
          // no registered avatar for the hash.
          onError={() => setImgFailed(true)}
          className="block w-full h-full object-cover"
        />
      ) : (
        <MonogramFallback
          kind="avatar"
          title={username}
          data-testid={`${rest['data-testid'] ?? 'avatar'}-monogram`}
        />
      )}
    </div>
  );
}
