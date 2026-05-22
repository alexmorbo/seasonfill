import { describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useCmdK } from './use-cmdk';

describe('useCmdK', () => {
  it('fires onTrigger on Meta+K and prevents default', () => {
    const cb = vi.fn();
    renderHook(() => useCmdK(cb));
    const ev = new KeyboardEvent('keydown', { key: 'k', metaKey: true, cancelable: true });
    window.dispatchEvent(ev);
    expect(cb).toHaveBeenCalledTimes(1);
    expect(ev.defaultPrevented).toBe(true);
  });
});
