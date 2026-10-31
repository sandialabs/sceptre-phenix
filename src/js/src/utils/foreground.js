// Whether the user may be looking at this page: its browser tab is showing,
// whether or not its window has focus, as on a wall display or a second
// monitor. Background refreshes that make the server query minimega skip their
// turn otherwise, since minimega runs one command at a time and every query
// delays the user's own actions.
export function inForeground() {
  return !document.hidden;
}
