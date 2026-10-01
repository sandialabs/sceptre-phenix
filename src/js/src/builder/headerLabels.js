/**
 * The labels of the editor header's buttons. As the header narrows they go
 * in two steps, as the drafts' header's do (see .builder-header__label in
 * builder.css). The editor header also takes a step sooner when its labels
 * would move the counts from where the header puts them with no labels:
 * centered on it, or as near as Back to drafts, the name and the buttons
 * let them be. So the counts stay centered, and the buttons show their
 * labels only in the room beside them.
 *
 * data-labels on the header says which labels it may show: `all`, `some`
 * (Reset view and Commands, without its keys) or `none`.
 */

// From the most labels to none.
const STEPS = ['all', 'some', 'none'];

// Where the counts are, and how tall the header is: the labels of a step
// that changes either would move the counts or wrap the header.
function place(header, counts) {
  const box = counts.getBoundingClientRect();

  return [box.left, box.top, box.right, box.bottom, header.offsetHeight];
}

function samePlace(a, b) {
  return a.every((value, index) => Math.abs(value - b[index]) < 0.5);
}

/**
 * Shows the most labels that leave the counts where they are with none.
 * Each step is laid out and measured in turn, before the page is drawn.
 *
 * @param {HTMLElement} header the editor header
 * @returns {string} the step taken, or '' without counts to keep in place
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
 * Fits the header's labels while it is shown: when the page it is on
 * changes size, and when its text changes (the name, the counts, the
 * checks, the theme or a key). Neither changes with the labels, so fitting
 * them never asks to fit them again.
 *
 * @param {HTMLElement} header the editor header
 * @param {HTMLElement} page the element the header is in, which the window
 *   sizes
 * @returns {() => void} stops following
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
