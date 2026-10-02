// Whether the user is looking at this page: its browser tab is showing and
// its window has focus. Background refreshes that make the server query
// minimega skip their turn otherwise, since minimega runs one command at a
// time and every query delays the user's own actions.
export function inForeground() {
  return !document.hidden && document.hasFocus();
}
