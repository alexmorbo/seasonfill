import { describe, expect, it } from 'vitest';
import { buildPosterUrl } from './posters';

describe('buildPosterUrl', () => {
  it('defaults to full size', () => {
    expect(buildPosterUrl('homelab', 122)).toBe(
      '/api/v1/instances/homelab/series/122/poster?size=full',
    );
  });

  it('honours small size', () => {
    expect(buildPosterUrl('homelab', 122, 'small')).toBe(
      '/api/v1/instances/homelab/series/122/poster?size=small',
    );
  });

  it('url-encodes the instance name', () => {
    expect(buildPosterUrl('my instance/x', 7)).toBe(
      '/api/v1/instances/my%20instance%2Fx/series/7/poster?size=full',
    );
  });
});
