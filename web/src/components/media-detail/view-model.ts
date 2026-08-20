import type { ReactNode, RefObject } from 'react';
import type {
  RatingScore,
  StatusToken,
  CastMember,
  ContentRatingBadge,
  ExternalLinks,
} from '@/api/series';

/** Discriminator — drives the hero root `data-testid` and future type-only branches. */
export type MediaType = 'series' | 'movie';

/**
 * Status token vocabulary. U-4 = series set verbatim (from api/series.ts StatusToken).
 * Movie tokens (`released`, `post_production`, …) are added in U-5/U-6 — NOT here.
 * Kept as an alias so U-5 can widen it in one place.
 */
export type MediaStatusToken = StatusToken; // 'continuing'|'ended'|'canceled'|'in_production'|'upcoming'|'unknown'

/** A content-addressed media hash (feed to `mediaUrl()`), already resolved or undefined. */
export type MediaAsset = string | undefined;

/** One ★ rating source row. Mirrors RatingDuo/RatingsSection inputs. */
export interface MediaRating {
  readonly source: 'tmdb' | 'imdb';
  readonly score: number;            // > 0 (caller filters absent)
  readonly votes?: number | undefined;
  readonly staleAt?: string | undefined;   // ISO; renders <StaleBadge>
}

/** Ratings block for the hero ★ duo + the ratings section. */
export interface MediaRatings {
  readonly tmdb?: RatingScore | undefined;   // effective (live-deduped) hero ★
  readonly imdb?: RatingScore | undefined;
  readonly tmdbStaleAt?: string | undefined;
  readonly imdbStaleAt?: string | undefined;
  readonly imdbLoading?: boolean | undefined;
  // Ratings-SECTION surface (under cast). OMDb content-rating + awards.
  readonly rated?: string | undefined;       // OMDb content-rating (e.g. "TV-MA")
  readonly awards?: string | undefined;
  // U-4 sub-step B — the ratings-SECTION's own tmdb/imdb numbers are a
  // DISTINCT resolution from the hero's `tmdb`/`imdb` above: the hero
  // falls back to the skeleton rating for instant first paint (#1059),
  // while the section (mirrors the old data-only `RatingsSection`) shows
  // nothing until the live /ratings query resolves. Keeping them separate
  // preserves that byte-identical timing.
  readonly sectionTmdbRating?: number | undefined;
  readonly sectionTmdbVotes?: number | undefined;
  readonly sectionImdbRating?: number | undefined;
  readonly sectionImdbVotes?: number | undefined;
}

/** A hero action button. `kind:'node'` passes a ready-made element (FollowButton, split-menu). */
export interface MediaAction {
  readonly id: string;
  readonly kind: 'node';
  readonly node: ReactNode;
}

/** One "open in <instance>" / "add to <instance>" hero split-menu row. */
export interface MediaHeroMenuItem {
  readonly name: string;
  readonly href?: string | undefined;
}

/**
 * U-4 sub-step B — the hero's split-button + follow/monitored/ellipsis row.
 * The instance/href RESOLUTION (publicUrlByName, openItems/addItems,
 * showCaret) lives in the series/movie adapter; `MediaHero` only renders
 * the already-resolved data (§3.1 "Chosen approach" — keeps the
 * `hero-menu-*` / `hero-action-caret` DOM byte-identical without flattening
 * into a generic `MediaAction[]`, which stays reserved for simpler U-6
 * movie buttons).
 */
export interface MediaHeroActions {
  readonly backHref: string;
  readonly backLabel: string;              // already i18n-resolved by the adapter
  readonly sonarrHref?: string | undefined;
  readonly showAddToSonarr: boolean;
  readonly showCaret: boolean;
  readonly openItems: readonly MediaHeroMenuItem[];
  readonly addItems: readonly string[];
  readonly onAddToSonarr: () => void;
  readonly onAddToInstance: (name: string) => void;
  readonly followButton: ReactNode;        // <FollowButton seriesId=…/> (series); undefined for movie in U-4
}

/** One sidebar (rail) fact row — REPLACES RailCard's hard-coded series rows. */
export interface MediaFact {
  readonly id: string;               // stable key
  readonly label: string;            // already i18n-resolved by the adapter
  readonly value: ReactNode;         // text or element (logo <img>, <CountryName>, …)
  readonly accent?: boolean | undefined;   // e.g. status === 'continuing'
  readonly testId?: string | undefined;    // preserve existing rail-row-* testids
}

/** A keyword chip. */
export interface MediaKeyword {
  readonly id?: number | undefined;
  readonly name?: string | undefined;
}

/** Cast block. */
export interface MediaCast {
  readonly members: readonly CastMember[];
  readonly href: string;             // "view all" link (series: /series/:id/cast)
  // U-4 §3.4: `MediaCastStrip`'s loading-section emits `data-series-id`,
  // which must stay byte-identical to the current series card's loading
  // DOM — so this stays a plain numeric id, fed straight into that prop.
  readonly mediaId: number;
  readonly limit?: number | undefined;
  readonly loading?: boolean | undefined;   // degraded skeleton
  readonly servedLang?: string | undefined; // castServedLang — reserved for a future EN badge; unused in U-4 render
}

/** Overview block (localized text + language-fallback signal). */
export interface MediaOverview {
  readonly label: string;            // i18n-resolved section label
  readonly text: string;             // resolved (already falls back to empty-state string)
  readonly contentLang?: string | undefined;
  readonly requestedLang?: string | undefined;
  readonly loading?: boolean | undefined;
}

/** Recommendations rail — presentational data + render delegate. */
export interface MediaRecommendations {
  readonly items: readonly RecommendationItem[];
  readonly isLoading: boolean;
  readonly visible: boolean;
  readonly sentinelRef: RefObject<HTMLElement | null>;
  readonly renderCard: (item: RecommendationItem, idx: number) => ReactNode;
  readonly staleBadge?: ReactNode;
}
/** Loose row — series recs today; movie recs in U-6 map onto the same shape. */
export interface RecommendationItem {
  readonly series_id?: number | undefined;
  readonly tmdb_series_id?: number | undefined;
  readonly title?: string | undefined;
  readonly year?: number | undefined;
  readonly poster_asset?: string | undefined;
  readonly tmdb_rating?: number | undefined;
  readonly in_library?: boolean | undefined;
}

/** Collection strip (movie belongs_to_collection). Optional — series leaves undefined in U-4. */
export interface MediaCollection {
  readonly node?: ReactNode;         // scaffold renders it verbatim if present
}

/** Trailer passthrough (hero play button + modal). */
export interface MediaTrailer {
  readonly key?: string | undefined;
  readonly site?: string | undefined;
  readonly name?: string | undefined;
}

export interface MediaDetailVM {
  readonly type: MediaType;

  // Identity / title block
  readonly localizedTitle: string;
  readonly originalTitle?: string | undefined;
  readonly tagline?: string | undefined;
  readonly statusToken: MediaStatusToken;
  readonly yearLabel: string;             // pre-computed (series: yearRange())
  readonly runtimeMinutes?: number | undefined;
  readonly contentRating?: ContentRatingBadge | undefined;   // TMDB badge (distinct from ratings.rated)
  readonly genres: readonly { readonly id?: number; readonly name?: string }[];

  // Media assets
  readonly posterAsset: MediaAsset;
  readonly backdropAsset: MediaAsset;
  readonly backdropLoadingLabel?: string | undefined;  // degraded plate label

  // Ratings
  readonly ratings: MediaRatings;

  // Hero actions (split menu + trailer + follow + monitored + ellipsis, prebuilt)
  readonly actions: readonly MediaAction[];
  readonly trailer?: MediaTrailer | undefined;
  // U-4 sub-step B — resolved split-button/back-link/follow-button data
  // for `MediaHero`'s action row (see `MediaHeroActions` above).
  readonly heroActions: MediaHeroActions;

  // Sidebar / rail
  readonly sidebarFacts: readonly MediaFact[];
  readonly keywords: readonly MediaKeyword[];

  // Cast / recommendations
  readonly cast: MediaCast;
  readonly recommendations: MediaRecommendations;

  // Collection / footer
  readonly collection?: MediaCollection | undefined;
  readonly externalLinks?: ExternalLinks | undefined;
  readonly overview: MediaOverview;

  // Freshness / degraded
  readonly syncedAt?: string | undefined;
  readonly degraded: readonly string[];   // DegradedSource[] sources for <DegradedChip>

  // Hero fallback discriminators (series: sonarr-only). Movie always false.
  readonly sonarrOnly: boolean;
}
