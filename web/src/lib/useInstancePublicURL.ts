import { useMemo } from 'react';
import { useInstances } from './instances';

// Returns the instance's browser-facing URL (PublicURL on dto.Instance —
// equivalent to UIURL on dto.InstanceDetail, derived server-side) or
// undefined when the instance has no public URL configured or the
// instances query hasn't resolved yet. SonarrLink hides the deep-link
// affordance when this is undefined so we never point operators at an
// internal-only Sonarr URL.
export function useInstancePublicURL(
  instanceName: string | null | undefined,
): string | undefined {
  const inst = useInstances();
  return useMemo(() => {
    if (!instanceName) return undefined;
    const rows = inst.data?.instances ?? [];
    const row = rows.find((i) => i.name === instanceName);
    const url = row?.public_url ?? '';
    return url.length > 0 ? url : undefined;
  }, [inst.data, instanceName]);
}
