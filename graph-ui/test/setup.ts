/**
 * jsdom lacks ResizeObserver — @react-three/fiber Canvas requires it
 * (via react-use-measure). WebGL itself is unavailable in jsdom, so R3F
 * scenes mount inertly; component tests assert state/UI, not pixels.
 */
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
}
