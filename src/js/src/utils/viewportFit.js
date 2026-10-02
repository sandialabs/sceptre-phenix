// The height, in pixels, that el can take without the page scrolling: what
// is left of the window below el's top once the page content under it, the
// main area's bottom padding and the footer have their room. el must be
// laid out (not hidden); returns null when it is not.
export function roomInWindow(el) {
  const main = el?.closest('main');
  const footer = document.querySelector('.app-footer');
  if (!main || !footer || el.offsetParent === null) return null;

  const bottom = (node) => node.getBoundingClientRect().bottom;
  const box = el.getBoundingClientRect();
  // the main area stretches to push the footer down, so measure the
  // page's content in it rather than the area itself
  const contentBottom = Math.max(...[...main.children].map(bottom));
  const mainStyle = getComputedStyle(main);
  const mainEnd =
    parseFloat(mainStyle.paddingBottom) +
    parseFloat(mainStyle.borderBottomWidth);
  // the footer's text floats outside its box, so measure every part
  const footerBox = footer.getBoundingClientRect();
  const footerHeight =
    Math.max(...[footer, ...footer.querySelectorAll('*')].map(bottom)) -
    footerBox.top +
    Math.max(0, footerBox.top - bottom(main));
  const below = contentBottom - box.bottom + mainEnd + footerHeight;
  return window.innerHeight - (box.top + window.scrollY) - below;
}
