// Diagnostic logging for development builds only; production builds drop it
// so the browser console shows only warnings and errors.
export const debug = import.meta.env.DEV
  ? console.debug.bind(console) // eslint-disable-line no-console
  : () => {};
