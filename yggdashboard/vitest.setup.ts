import '@testing-library/jest-dom/vitest';

// jsdom has no ResizeObserver implementation. NetworkGraph.svelte uses
// bind:clientWidth (Svelte 5 compiles that to a real `new
// ResizeObserver(...)` call) to size itself responsively - without this
// stub, mounting it under jsdom throws "ResizeObserver is not defined"
// before any assertion runs. A no-op is fine: the component seeds a
// sensible default width and only needs an observer that doesn't crash.
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}
