/**
 * The labels of the editor header's buttons, which go in two steps as the
 * header narrows (see .builder-header__label in builder.css). The editor
 * header steps sooner to keep its counts centered.
 */

// From the most labels to none.
const STEPS = ['all', 'some', 'none'];

// The position of the counts and the height of the header. A step whose
// labels change either would move the counts or wrap the header.
function place(header, counts) {
  const box = counts.getBoundingClientRect();

  return [box.left, box.top, box.right, box.bottom, header.offsetHeight];
}

function samePlace(a, b) {
  return a.every((value, index) => Math.abs(value - b[index]) < 0.5);
}

/**
 * Shows the most labels that keep the counts in place. Each step is laid out
 * and measured before the page is drawn.
 */
export function fitHeaderLabels(header) {
  const counts = header?.querySelector(':scope > .builder-counts');

  if (!counts) {
    return '';
  }

  header.dataset.labels = 'none';
  const home = place(header, counts);

  for (const step of STEPS.slice(0, -1)) {
    header.dataset.labels = step;

    if (samePlace(place(header, counts), home)) {
      return step;
    }
  }

  header.dataset.labels = 'none';

  return 'none';
}

/**
 * Fits the header's labels when the page resizes or the header's text
 * changes. The labels change neither, so a fit never causes another fit.
 */
export function followHeaderLabels(header, page) {
  if (
    typeof ResizeObserver === 'undefined' ||
    typeof MutationObserver === 'undefined'
  ) {
    return () => {};
  }

  const fit = () => fitHeaderLabels(header);
  // It reports the page's size once when it starts, which fits the labels
  // before the header is first drawn.
  const resized = new ResizeObserver(fit);
  const changed = new MutationObserver(fit);

  resized.observe(page);
  changed.observe(header, {
    childList: true,
    characterData: true,
    subtree: true,
  });

  return () => {
    resized.disconnect();
    changed.disconnect();
  };
}
