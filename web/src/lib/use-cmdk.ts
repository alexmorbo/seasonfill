import { useEffect } from 'react';

export function useCmdK(onTrigger: () => void): void {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        const t = e.target as HTMLElement | null;
        const tag = t?.tagName;
        // Don't hijack ⌘K inside text inputs — but the prototype does, so do too.
        // The brief explicitly says ⌘K opens NewScanModal globally.
        e.preventDefault();
        onTrigger();
        // ESLint-style narrow: keep tag readable as an intentional no-op.
        void tag;
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onTrigger]);
}
