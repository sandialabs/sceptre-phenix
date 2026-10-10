/**
 * The labels of the editor header's buttons. As the header narrows, the
 * labels go in two steps, as in the drafts header (see .builder-header__label
 * in builder.css). The editor header also takes a step sooner when its labels
 * would move the counts from their position with no labels. That position is
 * the center of the header, or as near to it as Back to drafts, the name and
 * the buttons allow. Thus the counts stay centered, and the buttons show
 * their labels only in the space beside them.
 *
 * data-labels on the header tells which labels it can show: `all`, `some`
 * (Reset view and Commands, without its keys) or `none`.
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
 * Shows the most labels that keep the counts at their position with no
 * labels. Each step is laid out and measured in turn, before the page is
 * drawn.
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
 * Fits the header's labels while the header shows. It fits them when the
 * page that holds the header changes size, and when the header's text
 * changes (the name, the counts, the checks, the theme or a key). The labels
 * change neither, so fitting them never causes another fit.
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
