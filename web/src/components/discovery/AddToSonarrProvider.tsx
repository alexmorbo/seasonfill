// S5 / ADR-0008: app-shell-level provider for the Add-to-Sonarr flow. Holds
// the single `target` and renders exactly ONE <AddToSonarrModal> in its own
// subtree — a sibling of the router content, never a descendant of any
// discovery card. This structural placement is what makes the S4-A
// propagation race impossible: no modal-internal event can bubble (through
// the React tree) to a card's onClick, because no card is in the modal's
// React ancestry.

import { useCallback, useMemo, useState, type ReactNode } from 'react';
import {
  AddToSonarrCtx,
  type AddToSonarrLauncher,
  type AddToSonarrTarget,
} from './add-to-sonarr-context';
import { AddToSonarrModal } from './AddToSonarrModal';

export function AddToSonarrProvider({
  children,
}: {
  readonly children: ReactNode;
}) {
  const [target, setTarget] = useState<AddToSonarrTarget | null>(null);

  const openAddToSonarr = useCallback((next: AddToSonarrTarget) => {
    setTarget(next);
  }, []);
  const close = useCallback(() => setTarget(null), []);

  const value = useMemo<AddToSonarrLauncher>(
    () => ({ target, openAddToSonarr, close }),
    [target, openAddToSonarr, close],
  );

  return (
    <AddToSonarrCtx.Provider value={value}>
      {children}
      {target !== null && (
        // Mount only while open → every open is a fresh mount with clean
        // useState. The `key` remounts if the target changes while open
        // (defensive; the normal flow closes → unmounts first).
        <AddToSonarrModal
          key={target.tvdbId ?? target.tmdbId ?? target.title}
          target={target}
          onClose={close}
        />
      )}
    </AddToSonarrCtx.Provider>
  );
}
