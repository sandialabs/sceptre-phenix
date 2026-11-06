// What the Console page's toolbar offers: the terminal's colors and text
// size, the minimega commands it types, how it reconnects, and how it reads
// the lines the user enters.
import { readPref, writePref } from '@/utils/prefs.js';

export const MINIMEGA_REFERENCE =
  'https://sandia-minimega.github.io/minimega/reference/minimega/';

// The commands the Commands menu types. Those that only read (`run`) are
// also entered; the others wait for the user to finish and enter them.
export const MINIMEGA_COMMANDS = [
  { command: 'vm info', hint: 'Each VM and its state', run: true },
  { command: 'vm info summary', hint: 'A short table of the VMs', run: true },
  { command: 'namespace', hint: 'The namespaces', run: true },
  { command: 'ns hosts', hint: 'The hosts of this namespace', run: true },
  { command: 'host', hint: 'Load and memory of each host', run: true },
  { command: 'mesh status', hint: 'The mesh of minimega nodes', run: true },
  { command: 'vlans', hint: 'The VLAN aliases', run: true },
  { command: 'cc clients', hint: 'The miniccc clients', run: true },
  { command: 'cc commands', hint: 'The cc commands sent', run: true },
  { command: 'version', hint: 'The minimega version', run: true },
  { command: 'help', hint: 'The minimega commands', run: true },
  { command: 'vm start ', hint: 'Start VMs: type a VM target', run: false },
  { command: 'vm stop ', hint: 'Stop VMs: type a VM target', run: false },
  { command: 'cc exec ', hint: 'Run a command on VMs', run: false },
];

// Seconds to wait before each try to reconnect a lost console.
export const RECONNECT_DELAYS = [1, 2, 4, 8, 15];

// the wait before try number `attempt` (from 0), or null after the last
export function reconnectDelay(attempt) {
  return RECONNECT_DELAYS[attempt] ?? null;
}

// Each text color has a contrast of 7:1 or more on its background.
const DARK_THEME = {
  background: '#2b2b2b',
  foreground: '#f2f2f2',
  cursor: '#f2f2f2',
  cursorAccent: '#2b2b2b',
  selectionBackground: '#3d5a80',
  selectionForeground: '#ffffff',
  black: '#2b2b2b',
  red: '#ff9e9e',
  green: '#8ee89a',
  yellow: '#f2cc60',
  blue: '#9ec2ff',
  magenta: '#e5b0ff',
  cyan: '#80e6ec',
  white: '#e6e6e6',
  brightBlack: '#bababa',
  brightRed: '#ff9e9e',
  brightGreen: '#8ee89a',
  brightYellow: '#f2cc60',
  brightBlue: '#9ec2ff',
  brightMagenta: '#e5b0ff',
  brightCyan: '#80e6ec',
  brightWhite: '#ffffff',
};

const LIGHT_THEME = {
  background: '#fafafa',
  foreground: '#1f1f1f',
  cursor: '#1f1f1f',
  cursorAccent: '#fafafa',
  selectionBackground: '#b6d4fb',
  selectionForeground: '#000000',
  black: '#1f1f1f',
  red: '#a1121b',
  green: '#116329',
  yellow: '#6b4f00',
  blue: '#0b4fa8',
  magenta: '#7a2a9c',
  cyan: '#085a64',
  white: '#fafafa',
  brightBlack: '#4d4d4d',
  brightRed: '#a1121b',
  brightGreen: '#116329',
  brightYellow: '#6b4f00',
  brightBlue: '#0b4fa8',
  brightMagenta: '#7a2a9c',
  brightCyan: '#085a64',
  brightWhite: '#000000',
};

export function terminalTheme(dark) {
  return dark ? DARK_THEME : LIGHT_THEME;
}

// Whether the terminal is dark: the app is dark, so it is dark unless the
// page sets data-theme="light".
export function darkTerminal() {
  return document.documentElement.dataset.theme !== 'light';
}

// Calls listener(dark) when the page's data-theme changes; returns a
// function that stops it.
export function watchPageTheme(listener) {
  if (typeof MutationObserver !== 'function') return () => {};
  const observer = new MutationObserver(() => listener(darkTerminal()));
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme'],
  });
  return () => observer.disconnect();
}

export const FONT_SIZE = { min: 10, max: 24, default: 14 };
const FONT_SIZE_KEY = 'phenix.console.fontSize';

export function clampFontSize(size) {
  const n = Math.round(Number(size));
  if (!Number.isFinite(n)) return FONT_SIZE.default;
  return Math.min(FONT_SIZE.max, Math.max(FONT_SIZE.min, n));
}

export function readFontSize() {
  const stored = readPref(FONT_SIZE_KEY);
  return stored === null ? FONT_SIZE.default : clampFontSize(stored);
}

export function writeFontSize(size) {
  writePref(FONT_SIZE_KEY, clampFontSize(size));
}

// What `quit` the line enters, if any: 'confirm' for `quit`, which minimega
// asks about again, or 'now' for `quit <delay>`, which it does not.
export function quitCommand(line) {
  const words = (line ?? '').trim().split(/\s+/);
  if (words[0] !== 'quit') return null;
  return words.length === 1 ? 'confirm' : 'now';
}

// the text after minimega's prompt (`minimega:<socket>[<namespace>]$ `) in
// a terminal row, or null when the row has no prompt
export function afterPrompt(row) {
  const match = /^minimega:[^$]*\$ ?(.*)$/.exec(row ?? '');
  return match ? match[1] : null;
}

// the end of an escape sequence that starts at data[i]
function escapeEnd(data, i) {
  if (data[i + 1] === '[') {
    let j = i + 2;
    while (j < data.length && !/[@-~]/.test(data[j])) j++;
    return j + 1;
  }
  return data[i + 1] === 'O' ? i + 3 : i + 2;
}

// Follows the line the user types, from the terminal's input, so the page
// can see what each Enter submits. Keys that move the cursor, recall
// history or complete a word make the line unknown until the next Enter.
export class LineTracker {
  line = '';

  // Returns each line that data submits: its text, or null when unknown.
  feed(data) {
    const submitted = [];
    for (let i = 0; i < data.length; ) {
      const ch = data[i];
      if (ch === '\x1b') {
        this.line = null;
        i = escapeEnd(data, i);
        continue;
      }
      i++;
      if (ch === '\r' || ch === '\n') {
        submitted.push(this.line);
        this.line = '';
      } else if (ch === '\x7f' || ch === '\b') {
        this.line = this.line === null ? null : this.line.slice(0, -1);
      } else if (ch === '\x03' || ch === '\x15') {
        this.line = '';
      } else if (ch < ' ') {
        this.line = null;
      } else if (this.line !== null) {
        this.line += ch;
      }
    }
    return submitted;
  }
}

// A terminal buffer's text: each line once, a wrapped line joined to the
// one before it, with no blank lines at the end.
export function bufferText(buffer) {
  const lines = [];
  for (let y = 0; y < buffer.length; y++) {
    const line = buffer.getLine(y);
    if (!line) continue;
    const text = line.translateToString(true);
    if (line.isWrapped && lines.length) {
      lines[lines.length - 1] += text;
    } else {
      lines.push(text);
    }
  }
  while (lines.length && lines.at(-1).trim() === '') lines.pop();
  return lines.join('\n');
}

const pad = (n) => String(n).padStart(2, '0');

// the name a transcript downloads as, such as
// minimega-console-demo-2026-11-04-153012.txt
export function transcriptName(date, namespace) {
  const day = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  const time = `${pad(date.getHours())}${pad(date.getMinutes())}${pad(date.getSeconds())}`;
  const where = namespace ? `${namespace.replace(/[^\w.-]+/g, '_')}-` : '';
  return `minimega-console-${where}${day}-${time}.txt`;
}

// Copies text to the clipboard, and reports whether it did. The page has no
// Clipboard API when it is served over plain http, so it then copies the
// selection of a text area that is not shown.
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.append(area);
    area.select();
    try {
      return document.execCommand?.('copy') === true;
    } catch {
      return false;
    } finally {
      area.remove();
    }
  }
}

// whether this browser runs on macOS, where Cmd, not Ctrl, copies and pastes
export function onMac() {
  const platform =
    navigator.userAgentData?.platform ?? navigator.platform ?? '';
  return /mac/i.test(platform);
}
