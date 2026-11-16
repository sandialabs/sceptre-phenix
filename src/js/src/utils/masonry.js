// v-masonry gives each item of a CSS grid a span of short rows that fits its
// height, so an item goes up under the item above it. The order stays row by
// row. Once laid out, the grid has the class is-masonry: its CSS then sets
// grid-auto-rows to MASONRY_ROW pixels and row-gap to 0, and each span holds
// the column gap. Without ResizeObserver, the grid stays a plain grid.

export const MASONRY_ROW = 4;

// Gives each item of grid the span of rows that its height needs.
export function layOut(grid) {
  const gap = parseFloat(getComputedStyle(grid).columnGap) || 0;
  for (const item of grid.children) {
    const height = item.getBoundingClientRect().height;
    const span = Math.max(1, Math.ceil((height + gap) / MASONRY_ROW));
    item.style.gridRowEnd = `span ${span}`;
  }
  grid.classList.add('is-masonry');
}

const state = new WeakMap();

export const vMasonry = {
  mounted(grid) {
    if (typeof ResizeObserver === 'undefined') return;
    // only the items are watched: a change to the grid's own height comes
    // from the spans and must not start a new layout
    const sizes = new ResizeObserver(() => layOut(grid));
    const watchItems = () => {
      sizes.disconnect();
      for (const item of grid.children) sizes.observe(item);
    };
    const items = new MutationObserver(() => {
      watchItems();
      layOut(grid);
    });
    items.observe(grid, { childList: true });
    watchItems();
    state.set(grid, { sizes, items });
  },
  unmounted(grid) {
    const s = state.get(grid);
    s?.sizes.disconnect();
    s?.items.disconnect();
    state.delete(grid);
  },
};
