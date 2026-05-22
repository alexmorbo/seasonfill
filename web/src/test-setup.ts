import '@testing-library/jest-dom/vitest';

// jsdom polyfills for Radix UI primitives (used by shadcn Select, RadioGroup, Dialog).
if (typeof window !== 'undefined') {
  const proto = Element.prototype as unknown as Record<string, unknown>;
  if (typeof proto.hasPointerCapture !== 'function') {
    proto.hasPointerCapture = () => false;
  }
  if (typeof proto.setPointerCapture !== 'function') {
    proto.setPointerCapture = () => undefined;
  }
  if (typeof proto.releasePointerCapture !== 'function') {
    proto.releasePointerCapture = () => undefined;
  }
  if (typeof proto.scrollIntoView !== 'function') {
    proto.scrollIntoView = () => undefined;
  }
  // ResizeObserver is required by Radix RadioGroup (use-size).
  const g = globalThis as unknown as { ResizeObserver?: unknown };
  if (typeof g.ResizeObserver === 'undefined') {
    g.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
}
