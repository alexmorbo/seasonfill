// Timezone context — single source of truth for "the timezone the server
// operator configured". Hydrated from /api/v1/settings/timezone (story 301).
//
// Why a context (not direct cache reads): formatDate() needs to be a
// synchronous string-returning function callable from non-component code
// paths (e.g. format-helpers in lib/). The Provider also captures the
// current zone into a module-level ref so the imperative formatDate(d, f)
// works the same as the hook-based useFormatDate() return.
//
// Fallback chain: API value → browser-resolved zone → 'UTC'.

import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import i18n from '@/i18n';
import { api, ApiError } from './api';

export interface TimezoneState {
  readonly timezone: string;        // IANA name actually used for formatting
  readonly source: 'db' | 'env' | 'default' | 'fallback';
  readonly requiresRestart: boolean;
}

interface TimezoneApiResponse {
  timezone?: string;
  source?: string;
  requires_restart?: boolean;
}

export const timezoneSettingKey = ['settings', 'timezone'] as const;

export async function fetchTimezoneSetting(): Promise<TimezoneState> {
  try {
    const res = await api<TimezoneApiResponse>('/settings/timezone');
    const tz = (res.timezone ?? '').trim();
    if (!tz) return { timezone: browserTz(), source: 'fallback', requiresRestart: false };
    return {
      timezone: tz,
      source: normalizeSource(res.source),
      requiresRestart: Boolean(res.requires_restart),
    };
  } catch (err) {
    // Backend may be older than story 301 (404), or temporarily down.
    // Fall back rather than blocking every formatDate() in the app.
    if (err instanceof ApiError && err.status !== 401) {
      return { timezone: browserTz(), source: 'fallback', requiresRestart: false };
    }
    throw err;
  }
}

function normalizeSource(s: string | undefined): TimezoneState['source'] {
  if (s === 'db' || s === 'env' || s === 'default') return s;
  return 'fallback';
}

function browserTz(): string {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return tz || 'UTC';
  } catch {
    return 'UTC';
  }
}

// Module-level ref so non-React callers (lib helpers, sort comparators
// that want to log a date) can call formatDate() without threading the
// hook. The Provider keeps this in sync with the React tree.
let currentTimezone: string = browserTz();

// ----------------------------------------------------------------------
// Format presets
// ----------------------------------------------------------------------

export type FormatPreset =
  | 'date'              // e.g. "Mar 15, 2026" (lng locale)
  | 'mediumDate'        // alias for 'date'
  | 'monthDay'          // "Mar 15"
  | 'time'              // "14:32" (24h)
  | 'datetime'          // "Mar 15, 2026, 14:32"
  | 'longDateTime'      // "March 15, 2026 at 2:32 PM"
  | 'shortDateTime';    // "15.03 14:32"  — used by watchdog cooldown chips

const PRESETS: Record<FormatPreset, Intl.DateTimeFormatOptions> = {
  date:           { year: 'numeric', month: 'short', day: 'numeric' },
  mediumDate:     { year: 'numeric', month: 'short', day: 'numeric' },
  monthDay:       { month: 'short', day: 'numeric' },
  time:           { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
  datetime:       { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
  longDateTime:   { year: 'numeric', month: 'long',  day: 'numeric', hour: '2-digit', minute: '2-digit' },
  // shortDateTime is non-standard "dd.MM HH:mm" — preserve the watchdog UI.
  // Built by hand below from Intl parts so the timezone still rotates.
  shortDateTime:  { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
};

function toDate(input: Date | string | number | null | undefined): Date | null {
  if (input == null) return null;
  if (input instanceof Date) return Number.isNaN(input.getTime()) ? null : input;
  const d = new Date(input);
  return Number.isNaN(d.getTime()) ? null : d;
}

export interface FormatDateOptions {
  readonly tz?: string;
  readonly locale?: string;
  readonly fallback?: string;
}

export function formatDate(
  input: Date | string | number | null | undefined,
  preset: FormatPreset = 'datetime',
  options: FormatDateOptions = {},
): string {
  const d = toDate(input);
  const fallback = options.fallback ?? '—';
  if (!d) return fallback;
  const tz = options.tz ?? currentTimezone;
  const locale = options.locale ?? i18n.resolvedLanguage ?? 'en-US';
  try {
    if (preset === 'shortDateTime') {
      // dd.MM HH:mm rendered with the chosen tz so cooldown chips agree
      // with the rest of the UI. Build from Intl.DateTimeFormat parts.
      const fmt = new Intl.DateTimeFormat('en-GB', { timeZone: tz, ...PRESETS.shortDateTime });
      const parts = fmt.formatToParts(d);
      const lookup = (t: Intl.DateTimeFormatPartTypes): string =>
        parts.find((p) => p.type === t)?.value ?? '';
      return `${lookup('day')}.${lookup('month')} ${lookup('hour')}:${lookup('minute')}`;
    }
    return new Intl.DateTimeFormat(locale, { timeZone: tz, ...PRESETS[preset] }).format(d);
  } catch {
    // Bad zone name (race with PATCH) — degrade to browser tz.
    if (preset === 'shortDateTime') {
      const fmt = new Intl.DateTimeFormat('en-GB', PRESETS.shortDateTime);
      const parts = fmt.formatToParts(d);
      const lookup = (t: Intl.DateTimeFormatPartTypes): string =>
        parts.find((p) => p.type === t)?.value ?? '';
      return `${lookup('day')}.${lookup('month')} ${lookup('hour')}:${lookup('minute')}`;
    }
    return new Intl.DateTimeFormat(locale, PRESETS[preset]).format(d);
  }
}

// currentHourIn returns the wall-clock hour 0..23 in the given (or
// configured) timezone. Used by HeroGreeting to pick morning / afternoon
// / evening based on the *operator's* day, not the viewer's browser.
export function currentHourIn(tz: string = currentTimezone, now: Date = new Date()): number {
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone: tz, hour: '2-digit', hourCycle: 'h23',
    }).formatToParts(now);
    const h = parts.find((p) => p.type === 'hour')?.value;
    const n = h ? Number(h) : NaN;
    return Number.isFinite(n) ? n : now.getHours();
  } catch {
    return now.getHours();
  }
}

// ----------------------------------------------------------------------
// React context
// ----------------------------------------------------------------------

interface TimezoneCtx {
  readonly state: TimezoneState;
  readonly isLoading: boolean;
}

const Ctx = createContext<TimezoneCtx | null>(null);

export function TimezoneProvider({ children }: { readonly children: ReactNode }) {
  const q = useQuery<TimezoneState, ApiError>({
    queryKey: timezoneSettingKey,
    queryFn: fetchTimezoneSetting,
    staleTime: 5 * 60_000,
    gcTime: 10 * 60_000,
    refetchOnMount: false,
  });

  const state: TimezoneState = q.data ?? {
    timezone: currentTimezone,
    source: 'fallback',
    requiresRestart: false,
  };

  // Keep the module-level ref in sync so the imperative formatDate()
  // callers (lib helpers, non-component code) see the same value.
  useEffect(() => {
    currentTimezone = state.timezone;
  }, [state.timezone]);

  const value = useMemo<TimezoneCtx>(
    () => ({ state, isLoading: q.isLoading }),
    [state, q.isLoading],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTimezoneState(): TimezoneCtx {
  const v = useContext(Ctx);
  if (!v) {
    // Fallback for tests that render a component without the Provider —
    // we don't want to crash; just return the module default.
    return {
      state: { timezone: currentTimezone, source: 'fallback', requiresRestart: false },
      isLoading: false,
    };
  }
  return v;
}

export function useTimezone(): string {
  return useTimezoneState().state.timezone;
}

// Primary use site for components. Returns a stable callable that
// captures the current tz at render time.
export function useFormatDate(): (
  input: Date | string | number | null | undefined,
  preset?: FormatPreset,
  overrides?: Omit<FormatDateOptions, 'tz'>,
) => string {
  const tz = useTimezone();
  return useMemo(
    () => (input, preset = 'datetime', overrides = {}) =>
      formatDate(input, preset, { ...overrides, tz }),
    [tz],
  );
}

// ----------------------------------------------------------------------
// IANA zone list (browser-derived with fallback)
// ----------------------------------------------------------------------

const FALLBACK_ZONES: readonly string[] = [
  'UTC', 'Europe/Moscow', 'Europe/Kaliningrad', 'Europe/Samara',
  'Europe/London', 'Europe/Berlin', 'Europe/Paris', 'Europe/Madrid',
  'Europe/Amsterdam', 'Europe/Warsaw', 'Europe/Kiev', 'Europe/Helsinki',
  'America/New_York', 'America/Chicago', 'America/Denver',
  'America/Los_Angeles', 'America/Sao_Paulo', 'America/Mexico_City',
  'Asia/Tokyo', 'Asia/Shanghai', 'Asia/Singapore', 'Asia/Dubai',
  'Asia/Hong_Kong', 'Asia/Seoul', 'Asia/Yekaterinburg',
  'Asia/Novosibirsk', 'Asia/Vladivostok',
  'Australia/Sydney', 'Australia/Perth', 'Pacific/Auckland',
];

export function listIANAZones(): readonly string[] {
  try {
    const supported = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] })
      .supportedValuesOf?.('timeZone');
    if (Array.isArray(supported) && supported.length > 0) return supported;
  } catch { /* fall through */ }
  return FALLBACK_ZONES;
}
