import '@testing-library/jest-dom/vitest';

// jsdom polyfills for Radix UI primitives (used by shadcn Select).
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
}
