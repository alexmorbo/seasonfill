import { describe, it, expect } from 'vitest';
import { formatMoney, isMoneyPresent } from './money';

describe('formatMoney', () => {
  it('formats whole USD with grouping and no decimals', () => {
    expect(formatMoney(85_000_000)).toBe('$85,000,000');
    expect(formatMoney(451_746_275)).toBe('$451,746,275');
  });

  it('formats small values', () => {
    expect(formatMoney(1)).toBe('$1');
  });
});

describe('isMoneyPresent', () => {
  it('is true for strictly positive numbers', () => {
    expect(isMoneyPresent(1)).toBe(true);
    expect(isMoneyPresent(85_000_000)).toBe(true);
  });

  it('is false for zero (no reported value) and undefined (unknown)', () => {
    expect(isMoneyPresent(0)).toBe(false);
    expect(isMoneyPresent(undefined)).toBe(false);
  });
});
