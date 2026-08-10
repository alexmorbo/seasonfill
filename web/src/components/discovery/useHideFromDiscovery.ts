import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAddBlocklist, useDeleteBlocklist } from '@/api/blocklist';

export interface HideArgs {
  readonly tmdbId: number;
  readonly title: string;
  /** Un-hide the card locally — invoked on Undo and on POST error. */
  readonly onRestore: () => void;
}

// useHideFromDiscovery encapsulates the hide-a-card flow: POST the tmdb ref to
// the blocklist, then raise a 5s Undo toast whose action DELETEs the created
// row (id from the POST response) and restores the card. On POST error we undo
// the optimistic removal and surface an error toast. Callers own the immediate
// optimistic removal (a local hidden-id set) — this hook only owns the network
// + toast lifecycle so it stays render-surface agnostic.
export function useHideFromDiscovery(): { hide: (args: HideArgs) => void } {
  const { t } = useTranslation();
  const add = useAddBlocklist();
  const del = useDeleteBlocklist();

  const hide = ({ tmdbId, title, onRestore }: HideArgs) => {
    add.mutate(
      { kind: 'tmdb', ref_id: tmdbId },
      {
        onSuccess: (created) => {
          toast(t('discovery.card.hidden', { title }), {
            duration: 5000,
            action: {
              label: t('discovery.card.undo'),
              onClick: () => {
                del.mutate(created.id);
                onRestore();
              },
            },
          });
        },
        onError: () => {
          onRestore();
          toast.error(t('discovery.card.hideFailed'));
        },
      },
    );
  };

  return { hide };
}
