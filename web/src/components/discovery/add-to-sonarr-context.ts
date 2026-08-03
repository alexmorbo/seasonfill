import { createContext, useContext } from 'react';

// AddToSonarrTarget — the normalized, surface-agnostic identity of a series
// to add. Decoupled from DiscoverySeriesItem so any surface (discovery cards
// today, series-detail page later) can build one from its own data.
export interface AddToSonarrTarget {
  readonly title: string;
  readonly tvdbId?: number;
  readonly tmdbId?: number;
}

export interface AddToSonarrLauncher {
  readonly target: AddToSonarrTarget | null;
  readonly openAddToSonarr: (target: AddToSonarrTarget) => void;
  readonly close: () => void;
}

export const AddToSonarrCtx = createContext<AddToSonarrLauncher | null>(null);

export function useAddToSonarrLauncher(): AddToSonarrLauncher {
  const ctx = useContext(AddToSonarrCtx);
  if (!ctx) {
    throw new Error(
      'useAddToSonarrLauncher must be used inside <AddToSonarrProvider>',
    );
  }
  return ctx;
}
