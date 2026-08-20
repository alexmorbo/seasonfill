import { useTranslation } from 'react-i18next';
import { Bookmark, BookmarkCheck } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  useFollowedIds,
  useFollowSeries,
  useUnfollowSeries,
  useFollowedMovieIds,
  useFollowMovie,
  useUnfollowMovie,
} from '@/api/follow';

interface FollowButtonSeriesProps {
  readonly mediaType?: 'series';
  readonly seriesId: number;
  /** compact = icon-only (card overlay); default = icon + label (hero). */
  readonly variant?: 'default' | 'compact';
}

interface FollowButtonMovieProps {
  readonly mediaType: 'movie';
  readonly tmdbId: number;
  /** compact = icon-only (card overlay); default = icon + label (hero). */
  readonly variant?: 'default' | 'compact';
}

export type FollowButtonProps = FollowButtonSeriesProps | FollowButtonMovieProps;

// FollowButton — the single follow/unfollow toggle used on the series-detail
// hero (labelled) and, in its compact icon-only form, as a poster overlay on
// the series card; also used on the movie hero. `mediaType` (default
// 'series') selects the series or movie follow hooks/endpoint. Dispatched to
// a small per-media-type impl component below so each mounted instance
// calls ONLY the hooks (and fires ONLY the /follow network query) its own
// media type needs — series call sites therefore keep byte-identical
// behavior/DOM to before this component grew a movie branch.
export function FollowButton(props: FollowButtonProps) {
  if (props.mediaType === 'movie') {
    return <MovieFollowButtonImpl tmdbId={props.tmdbId} variant={props.variant ?? 'default'} />;
  }
  return <SeriesFollowButtonImpl seriesId={props.seriesId} variant={props.variant ?? 'default'} />;
}

interface FollowButtonViewProps {
  readonly isFollowed: boolean;
  readonly pending: boolean;
  readonly label: string;
  readonly variant: 'default' | 'compact';
  readonly testId: string;
  readonly onClick: () => void;
}

function FollowButtonView({
  isFollowed, pending, label, variant, testId, onClick,
}: FollowButtonViewProps) {
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
      data-testid={testId}
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

function SeriesFollowButtonImpl({
  seriesId, variant,
}: {
  readonly seriesId: number;
  readonly variant: 'default' | 'compact';
}) {
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
    <FollowButtonView
      isFollowed={isFollowed}
      pending={pending}
      label={label}
      variant={variant}
      testId={`follow-button-${seriesId}`}
      onClick={onClick}
    />
  );
}

function MovieFollowButtonImpl({
  tmdbId, variant,
}: {
  readonly tmdbId: number;
  readonly variant: 'default' | 'compact';
}) {
  const { t } = useTranslation();
  const followedIds = useFollowedMovieIds();
  const follow = useFollowMovie();
  const unfollow = useUnfollowMovie();

  const isFollowed = followedIds.has(tmdbId);
  const pending = follow.isPending || unfollow.isPending;

  const onClick = () => {
    if (pending) return;
    if (isFollowed) unfollow.mutate({ tmdbId });
    else follow.mutate({ tmdbId });
  };

  const label = isFollowed ? t('follow.following') : t('follow.follow');

  return (
    <FollowButtonView
      isFollowed={isFollowed}
      pending={pending}
      label={label}
      variant={variant}
      testId={`follow-button-movie-${tmdbId}`}
      onClick={onClick}
    />
  );
}
