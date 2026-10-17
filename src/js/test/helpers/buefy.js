// A stand-in for the $buefy pages open dialogs, modals and toasts through,
// and readers for them; it imports only vitest, so it may be loaded early.
import { vi } from 'vitest';

// $buefy with every programmatic API the pages call recorded
export const fakeBuefy = () => ({
  dialog: { confirm: vi.fn(), alert: vi.fn(), prompt: vi.fn() },
  modal: { open: vi.fn() },
  toast: { open: vi.fn() },
});

// the options of the page's last confirm and alert dialogs
export const lastConfirm = (ctx) =>
  ctx.$buefy.dialog.confirm.mock.lastCall?.[0];
export const lastAlert = (ctx) => ctx.$buefy.dialog.alert.mock.lastCall?.[0];

// the options of each toast the page opened, in order
export const toasts = (ctx) =>
  ctx.$buefy.toast.open.mock.calls.map(([options]) => options);

// the options of each confirm, alert and prompt dialog the page opened, in
// the order it opened them
export function dialogs(ctx) {
  const { confirm, alert, prompt } = ctx.$buefy.dialog;
  return [confirm, alert, prompt]
    .flatMap(({ mock }) =>
      mock.calls.map(([options], i) => [mock.invocationCallOrder[i], options]),
    )
    .sort(([a], [b]) => a - b)
    .map(([, options]) => options);
}
