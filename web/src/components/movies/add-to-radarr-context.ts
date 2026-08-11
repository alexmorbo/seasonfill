import { createContext, useContext } from 'react';

// AddToRadarrTarget — the normalized, surface-agnostic identity of a movie to
// add. Clone of AddToSonarrTarget (movies are keyed by tmdb_id, no tvdb_id).
export interface AddToRadarrTarget {
  readonly title: string;
  readonly tmdbId?: number;
  readonly instanceName?: string;
}

export interface AddToRadarrLauncher {
  readonly target: AddToRadarrTarget | null;
  readonly openAddToRadarr: (target: AddToRadarrTarget) => void;
  readonly close: () => void;
}

export const AddToRadarrCtx = createContext<AddToRadarrLauncher | null>(null);

export function useAddToRadarrLauncher(): AddToRadarrLauncher {
  const ctx = useContext(AddToRadarrCtx);
  if (!ctx) {
    throw new Error(
      'useAddToRadarrLauncher must be used inside <AddToRadarrProvider>',
    );
  }
  return ctx;
}
