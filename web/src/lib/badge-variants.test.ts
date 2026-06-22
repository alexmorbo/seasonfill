import { describe, expect, it } from 'vitest';
import { healthKind, healthLabelKey } from './badge-variants';

describe('healthKind / healthLabelKey — Bootstrapping (Story 488 B-14)', () => {
  it('maps Bootstrapping to the neutral kind', () => {
    expect(healthKind('Bootstrapping')).toBe('neutral');
  });

  it('maps Bootstrapping to the i18n key health.bootstrapping', () => {
    expect(healthLabelKey('Bootstrapping')).toBe('health.bootstrapping');
  });

  it('preserves Available → success regression guard', () => {
    expect(healthKind('Available')).toBe('success');
    expect(healthLabelKey('Available')).toBe('health.available');
  });

  it('preserves SelfThrottled → warning regression guard', () => {
    expect(healthKind('SelfThrottled')).toBe('warning');
    expect(healthLabelKey('SelfThrottled')).toBe('health.selfThrottled');
  });

  it('preserves Unavailable* → danger regression guard', () => {
    expect(healthKind('UnavailableAuth')).toBe('danger');
    expect(healthKind('UnavailableNetwork')).toBe('danger');
    expect(healthKind('UnavailableUnknown')).toBe('danger');
  });
});
