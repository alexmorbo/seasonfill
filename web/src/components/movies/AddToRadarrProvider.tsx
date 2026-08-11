// App-shell-level provider for the Add-to-Radarr flow. Clone of
// AddToSonarrProvider: holds the single `target` and renders exactly ONE
// <AddToRadarrModal> as a sibling of the router content, never a descendant of
// any card — so no modal-internal event can bubble (through the React tree) to
// a card's onClick. Correct-by-construction (feedback_fe_interaction_live_verify).

import { useCallback, useMemo, useState, type ReactNode } from 'react';
import {
  AddToRadarrCtx,
  type AddToRadarrLauncher,
  type AddToRadarrTarget,
} from './add-to-radarr-context';
import { AddToRadarrModal } from './AddToRadarrModal';

export function AddToRadarrProvider({
  children,
}: {
  readonly children: ReactNode;
}) {
  const [target, setTarget] = useState<AddToRadarrTarget | null>(null);

  const openAddToRadarr = useCallback((next: AddToRadarrTarget) => {
    setTarget(next);
  }, []);
  const close = useCallback(() => setTarget(null), []);

  const value = useMemo<AddToRadarrLauncher>(
    () => ({ target, openAddToRadarr, close }),
    [target, openAddToRadarr, close],
  );

  return (
    <AddToRadarrCtx.Provider value={value}>
      {children}
      {target !== null && (
        // Mount only while open → every open is a fresh mount with clean
        // useState. The `key` remounts if the target changes while open.
        <AddToRadarrModal
          key={`${target.tmdbId ?? target.title}-${target.instanceName ?? ''}`}
          target={target}
          onClose={close}
        />
      )}
    </AddToRadarrCtx.Provider>
  );
}
