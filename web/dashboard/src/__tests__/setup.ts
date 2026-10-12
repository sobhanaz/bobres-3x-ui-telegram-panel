// jsdom lacks ResizeObserver, which some PrimeVue components start on mount;
// a component that measures nothing in tests is fine with one that never fires.
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
}
