// Which of a page's items are on screen, by key, as an IntersectionObserver
// reports them. Without one, every tracked item counts as on screen.

// how far outside the viewport an item counts as on screen, so what it shows
// is ready by the time it scrolls in
const MARGIN = '200px';

// Tracks items' elements; onShown(keys) is called with the keys of items that
// just came on screen.
export function trackVisibility(onShown) {
  const shown = new Set();
  const elements = new Map(); // by key
  const keys = new Map(); // by element

  const Observer = globalThis.IntersectionObserver;
  const observer = Observer
    ? new Observer(
        (entries) => {
          const appeared = [];
          for (const entry of entries) {
            const key = keys.get(entry.target);
            if (key === undefined) continue;
            if (!entry.isIntersecting) {
              shown.delete(key);
            } else if (!shown.has(key)) {
              shown.add(key);
              appeared.push(key);
            }
          }
          if (appeared.length) onShown(appeared);
        },
        { rootMargin: MARGIN },
      )
    : null;

  const forget = (key) => {
    const el = elements.get(key);
    if (!el) return;
    observer?.unobserve(el);
    keys.delete(el);
    elements.delete(key);
    shown.delete(key);
  };

  return {
    // Watches el as the item key's element, for a Vue function ref: called
    // again with the same element it does nothing, and with null it stops.
    track(key, el) {
      if (elements.get(key) === el) return;
      forget(key);
      if (!el) return;
      elements.set(key, el);
      keys.set(el, key);
      if (observer) {
        observer.observe(el);
      } else {
        shown.add(key);
        onShown([key]);
      }
    },

    isShown: (key) => shown.has(key),

    shownKeys: () => [...shown],

    stop() {
      observer?.disconnect();
      shown.clear();
      elements.clear();
      keys.clear();
    },
  };
}
