// jsdom lacks ResizeObserver, which assistant-ui's viewport hooks use.
if (typeof globalThis.ResizeObserver === 'undefined') {
  class ResizeObserverStub {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
}

// jsdom lacks element scrolling, which the thread viewport calls after mount.
if (typeof Element.prototype.scrollTo !== 'function') {
  Element.prototype.scrollTo = function scrollTo(): void {};
}
