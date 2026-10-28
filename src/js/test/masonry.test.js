import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MASONRY_ROW, layOut, vMasonry } from '@/utils/masonry.js';

// a grid with items of the heights given, and a column gap of gap pixels
function grid(heights, gap = 12) {
  const el = document.createElement('div');
  el.style.columnGap = `${gap}px`;
  for (const h of heights) {
    const item = document.createElement('article');
    item.getBoundingClientRect = () => ({ height: h });
    el.append(item);
  }
  document.body.append(el);
  return el;
}

const spans = (el) => [...el.children].map((item) => item.style.gridRowEnd);

describe('v-masonry', () => {
  let observers;

  beforeEach(() => {
    observers = [];
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback) {
          this.callback = callback;
          this.watched = new Set();
          observers.push(this);
        }
        observe(el) {
          this.watched.add(el);
        }
        disconnect() {
          this.watched.clear();
        }
      },
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.replaceChildren();
  });

  it('gives each item the rows that its height and the gap need', () => {
    const el = grid([100, 1030, 7]);
    layOut(el);
    expect(MASONRY_ROW).toBe(4);
    // (100 + 12) / 4 = 28, (1030 + 12) / 4 = 260.5, (7 + 12) / 4 = 4.75
    expect(spans(el)).toEqual(['span 28', 'span 261', 'span 5']);
    expect(el.classList.contains('is-masonry')).toBe(true);
  });

  it('watches the items, not the grid, and lays out again when one resizes', () => {
    const el = grid([40, 80]);
    vMasonry.mounted(el);
    const [sizes] = observers;
    expect([...sizes.watched]).toEqual([...el.children]);
    expect(el.classList.contains('is-masonry')).toBe(false);

    el.children[0].getBoundingClientRect = () => ({ height: 200 });
    sizes.callback();
    expect(spans(el)).toEqual(['span 53', 'span 23']);

    vMasonry.unmounted(el);
    expect(sizes.watched.size).toBe(0);
  });

  it('watches an item added later', async () => {
    const el = grid([40]);
    vMasonry.mounted(el);
    const added = document.createElement('article');
    added.getBoundingClientRect = () => ({ height: 20 });
    el.append(added);
    await new Promise((resolve) => setTimeout(resolve));
    expect(observers[0].watched.has(added)).toBe(true);
    expect(spans(el)).toEqual(['span 13', 'span 8']);
    vMasonry.unmounted(el);
  });

  it('leaves a plain grid without ResizeObserver', () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const el = grid([40]);
    vMasonry.mounted(el);
    expect(spans(el)).toEqual(['']);
    vMasonry.unmounted(el);
  });
});
