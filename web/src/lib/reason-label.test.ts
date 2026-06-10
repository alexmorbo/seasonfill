import { describe, it, expect, vi } from 'vitest';
import { resolveReasonLabel } from './reason-label';
import type { TFunction } from 'i18next';

const fakeT: TFunction = ((key: string, opts?: { defaultValue?: string }) => {
  const map: Record<string, string> = {
    'reasons.grab_selected': 'Захвачен',
    'reasons.skip_no_candidates_after_filter': 'Ни один кандидат не прошёл фильтры',
  };
  return map[key] ?? opts?.defaultValue ?? key;
}) as unknown as TFunction;

describe('resolveReasonLabel', () => {
  it('returns em-dash for null reason', () => {
    expect(resolveReasonLabel(null, fakeT)).toBe('—');
  });

  it('returns em-dash for undefined reason', () => {
    expect(resolveReasonLabel(undefined, fakeT)).toBe('—');
  });

  it('returns em-dash for empty string', () => {
    expect(resolveReasonLabel('', fakeT)).toBe('—');
  });

  it('hides skip_sonarr_handles as redundant with category chip', () => {
    expect(resolveReasonLabel('skip_sonarr_handles', fakeT)).toBe('');
  });

  it('hides skip_all_complete as redundant with category chip', () => {
    expect(resolveReasonLabel('skip_all_complete', fakeT)).toBe('');
  });

  it('hides skip_no_missing_episodes as redundant', () => {
    expect(resolveReasonLabel('skip_no_missing_episodes', fakeT)).toBe('');
  });

  it('returns localized text when translation exists', () => {
    expect(resolveReasonLabel('grab_selected', fakeT)).toBe('Захвачен');
    expect(resolveReasonLabel('skip_no_candidates_after_filter', fakeT)).toBe(
      'Ни один кандидат не прошёл фильтры',
    );
  });

  it('falls back to em-dash for unknown reason instead of leaking raw key', () => {
    expect(resolveReasonLabel('totally_unknown_reason', fakeT)).toBe('—');
  });

  it('does not call t for redundant reasons', () => {
    const spy = vi.fn();
    resolveReasonLabel('skip_sonarr_handles', spy as unknown as TFunction);
    expect(spy).not.toHaveBeenCalled();
  });
});
