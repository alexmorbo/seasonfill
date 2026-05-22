import { describe, expect, it } from 'vitest';
import { firstScanRunId, NoScanStartedError } from './scan-mutations';
import { DtoScanTriggerItemStatus } from '@/api/schema';

describe('firstScanRunId', () => {
  it('returns scan_run_id of the first element', () => {
    expect(
      firstScanRunId([
        {
          scan_run_id: 'abc-123',
          instance: 'alpha',
          status: DtoScanTriggerItemStatus.running,
        },
      ]),
    ).toBe('abc-123');
  });

  it('throws NoScanStartedError on empty array', () => {
    expect(() => firstScanRunId([])).toThrow(NoScanStartedError);
  });
});
