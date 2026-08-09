import { useTranslation } from 'react-i18next';
import { Bookmark, BookmarkCheck } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  useFollowedIds,
  useFollowSeries,
  useUnfollowSeries,
} from '@/api/follow';

interface FollowButtonProps {
  seriesId: number;
  /** compact = icon-only (card overlay); default = icon + label (hero). */
  variant?: 'default' | 'compact';
}

// FollowButton — the single follow/unfollow toggle used on the series-detail
// hero (labelled) and, in its compact icon-only form, as a poster overlay on
// the series card. Follow-state is derived from the shared /follow watchlist
// query (useFollowedIds), so every mounted button reflects the same Set and a
// single toggle invalidates them all at once.
export function FollowButton({ seriesId, variant = 'default' }: FollowButtonProps) {
  const { t } = useTranslation();
  const followedIds = useFollowedIds();
  const follow = useFollowSeries();
  const unfollow = useUnfollowSeries();

  const isFollowed = followedIds.has(seriesId);
  const pending = follow.isPending || unfollow.isPending;

  const onClick = () => {
    if (pending) return;
    if (isFollowed) unfollow.mutate({ seriesId });
    else follow.mutate({ seriesId });
  };

  const label = isFollowed ? t('follow.following') : t('follow.follow');

  return (
    <Button
      type="button"
      variant={isFollowed ? 'secondary' : 'outline'}
      size={variant === 'compact' ? 'icon' : 'sm'}
      disabled={pending}
      aria-pressed={isFollowed}
      aria-label={label}
      title={label}
      onClick={onClick}
      data-testid={`follow-button-${seriesId}`}
    >
      {isFollowed ? (
        <BookmarkCheck className="h-4 w-4" aria-hidden="true" />
      ) : (
        <Bookmark className="h-4 w-4" aria-hidden="true" />
      )}
      {variant === 'default' && <span>{label}</span>}
    </Button>
  );
}
