// The keyboard chord of a VNC console pane. The RFB canvas takes every key,
// so Ctrl+] and then one more key is the way out (WCAG 2.1.2), as in
// MiniTerminal:
//
// - Tab (or Shift+Tab) leaves the console.
// - 1–4 move keyboard input to that pane.
// - F exits fullscreen.
// - T hides or shows the top bar.
// - Esc cancels the chord.
import { readonly, ref } from 'vue';

/** The hint for the KEYBOARD tag tooltip, the Keys menu and the toast. */
export const CHORD_HINT = 'Ctrl+] then Tab to leave';

/** The key that starts the chord, as the shortcut list names it. */
export const CHORD_START = 'Ctrl+]';

/**
 * The keys after Ctrl+] and what each does, for the shortcut list of the
 * Keys panel. Keep it in step with handleKeydown.
 */
export const CHORD_KEYS = [
  { key: '1–4', does: 'Select that pane' },
  { key: 'F', does: 'Exit full screen' },
  { key: 'T', does: 'Show or hide the toolbar' },
  { key: 'Tab', does: 'Leave the console' },
  { key: 'Esc', does: 'Cancel the shortcut' },
];

const MODIFIERS = new Set(['Control', 'Shift', 'Alt', 'AltGraph', 'Meta']);
const PANE_KEY = /^(?:Digit|Numpad)([1-4])$/;

/**
 * Whether the event is Ctrl+]. It matches `event.code`, the physical key in
 * the US ] position, so the chord works on layouts where ] needs AltGr. On a
 * German keyboard that key is +.
 *
 * @param {KeyboardEvent} event
 */
export function isChordStart(event) {
  return (
    event.code === 'BracketRight' &&
    event.ctrlKey &&
    !event.altKey &&
    !event.metaKey &&
    !event.shiftKey
  );
}

function consume(event) {
  event.preventDefault();
  event.stopPropagation();
  return true;
}

/**
 * @typedef {object} ChordActions
 * @property {(options: { backward: boolean }) => void} [onLeave] move focus
 *   out of the console, to the top bar
 * @property {(pane: number) => void} [onSelectPane] give keyboard input to
 *   pane 1–4
 * @property {() => void} [onExitFullscreen]
 * @property {() => void} [onToggleToolbar] hide or show the top bar
 */

/**
 * The chord state of one pane.
 *
 * @param {ChordActions} actions
 * @returns {{ armed: Readonly<import('vue').Ref<boolean>>,
 *   handleKeydown: (event: KeyboardEvent) => boolean, disarm: () => void }}
 *   handleKeydown returns true when the chord used the key. It then calls
 *   preventDefault and stopPropagation, so the console does not get it.
 */
export function createChord(actions = {}) {
  const armed = ref(false);

  function handleKeydown(event) {
    if (!armed.value) {
      if (!isChordStart(event)) return false;
      armed.value = true;
      return consume(event);
    }
    if (MODIFIERS.has(event.key)) return false;

    armed.value = false;
    if (event.code === 'Tab' || event.key === 'Tab') {
      actions.onLeave?.({ backward: event.shiftKey });
      return consume(event);
    }
    const pane = PANE_KEY.exec(event.code);
    if (pane) {
      actions.onSelectPane?.(Number(pane[1]));
      return consume(event);
    }
    if (event.code === 'KeyF') {
      actions.onExitFullscreen?.();
      return consume(event);
    }
    if (event.code === 'KeyT') {
      actions.onToggleToolbar?.();
      return consume(event);
    }
    if (event.code === 'Escape' || event.key === 'Escape') {
      return consume(event);
    }
    return false;
  }

  return {
    armed: readonly(armed),
    handleKeydown,
    disarm: () => (armed.value = false),
  };
}

/**
 * Listens for the chord on a pane element in the capture phase, so the pane
 * gets each key before RFB does. Focus that leaves the pane ends the chord.
 *
 * @param {Element} pane
 * @param {ChordActions} actions
 * @returns {{ armed: Readonly<import('vue').Ref<boolean>>, detach: () => void }}
 */
export function attachChord(pane, actions) {
  const chord = createChord(actions);
  const onKeydown = (event) => chord.handleKeydown(event);
  const onFocusOut = () => chord.disarm();
  pane.addEventListener('keydown', onKeydown, { capture: true });
  pane.addEventListener('focusout', onFocusOut);
  return {
    armed: chord.armed,
    detach() {
      chord.disarm();
      pane.removeEventListener('keydown', onKeydown, { capture: true });
      pane.removeEventListener('focusout', onFocusOut);
    },
  };
}
