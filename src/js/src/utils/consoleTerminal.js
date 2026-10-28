// The terminal and websocket of a minimega console. A session outlives the
// Console page that shows it (see consoleSession.js): the page mounts its
// terminal when it opens and unmounts it when it closes, while the websocket
// stays open and the terminal keeps receiving output.
import { markRaw, ref } from 'vue';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { AttachAddon } from '@xterm/addon-attach';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';

import '@xterm/xterm/css/xterm.css';

import * as consoleApi from '@/api/console.js';
import {
  afterPrompt,
  bufferText,
  clampFontSize,
  LineTracker,
  darkTerminal,
  readFontSize,
  terminalTheme,
} from '@/components/console/consoleTools.js';
import {
  serverShuttingDown,
  shutdownWithin,
  wsCloseCause,
} from '@/utils/freshness.js';
import { gotNoResponse } from '@/utils/unreachable.js';
import { usePhenixStore } from '@/store.js';
import {
  currentConsole,
  endConsole,
  forgetConsolePid,
  rememberConsolePid,
  rememberedConsolePid,
  setCurrentConsole,
} from '@/utils/consoleSession.js';

// What openConsole is waiting on, so the page can say: 'reconnecting' while
// it checks on the console this browser had open, 'starting' while the
// server starts a new one.
export const consolePhase = ref(null);

// The server closes a websocket normally both when its console has ended and
// when it drops one that could not keep up with the output, so a normal close
// is checked against the server, which forgets an ended console first.
const NORMAL_CLOSE = 1000;

// the lines of output the terminal keeps above the screen
const SCROLLBACK = 10000;

let opening = null;

// Resolves to this tab's console session: the one it has open, else the one
// this browser had open if it is still running, else a new console, in the
// namespace of the experiment named (none for '').
export function openConsole(namespace = '') {
  const session = currentConsole();
  if (session) {
    return Promise.resolve(session);
  }

  opening ??= findOrStartConsole(namespace).finally(() => {
    opening = null;
    consolePhase.value = null;
  });
  return opening;
}

async function findOrStartConsole(wanted) {
  const username = usePhenixStore().username;
  let pid = rememberedConsolePid(username);
  let namespace = wanted;

  if (pid !== null) {
    consolePhase.value = 'reconnecting';
    const running = await consoleRunning(pid);
    if (running) {
      namespace = running.namespace ?? '';
    } else {
      forgetConsolePid(username, pid);
      pid = null;
    }
  }

  if (pid === null) {
    consolePhase.value = 'starting';
    const started = (await consoleApi.startConsole(wanted)).data;
    return startedSession(username, started, wanted);
  }

  const session = markRaw(new ConsoleSession(pid, username, namespace));
  setCurrentConsole(session);
  return session;
}

// Starts a new console in the namespace of the experiment named (none for
// ''), and only then ends the user's console: a failed start keeps it.
export async function replaceConsole(namespace = '') {
  consolePhase.value = 'starting';
  try {
    const started = (await consoleApi.startConsole(namespace)).data;
    await endConsole();
    return startedSession(usePhenixStore().username, started, namespace);
  } finally {
    consolePhase.value = null;
  }
}

// the session of a console the server started, remembered as the user's
function startedSession(username, started, wanted) {
  rememberConsolePid(username, started.pid);
  const session = markRaw(
    new ConsoleSession(started.pid, username, started.namespace ?? wanted),
  );
  setCurrentConsole(session);
  return session;
}

// the console's details while it runs, or null once it has ended
async function consoleRunning(pid) {
  try {
    return (await consoleApi.getConsole(pid)).data ?? {};
  } catch (err) {
    if (err.response?.status === 404) {
      return null;
    }
    throw err;
  }
}

function consoleWsUrl(pid) {
  const token = usePhenixStore().token;
  let path = `${import.meta.env.BASE_URL}api/v1/console/${pid}/ws`;
  if (token) {
    path += `?token=${encodeURIComponent(token)}`;
  }

  const proto = location.protocol === 'https:' ? 'wss://' : 'ws://';
  return proto + location.host + path;
}

class ConsoleSession {
  constructor(pid, username, namespace = '') {
    this.pid = pid;
    this.username = username;
    // the experiment whose minimega namespace the console uses, or ''
    this.namespace = namespace;
    // 'connecting', 'open', 'ended' (the console exited or was ended), or
    // 'lost' (the connection to it failed; the console may still be running)
    this.state = 'connecting';
    this.host = null;
    this.listeners = new Set();
    this.submitListeners = new Set();
    this.keyHandler = null;
    this.lines = new LineTracker();
    this.opened = false;
    this.disposed = false;
    this.attachAddon = null;

    // the terminal renders into this element, which moves between visits
    this.element = document.createElement('div');
    this.element.className = 'console-terminal';

    this.term = new Terminal({
      theme: terminalTheme(darkTerminal()),
      fontSize: readFontSize(),
      scrollback: SCROLLBACK,
    });
    this.fitAddon = new FitAddon();
    this.term.loadAddon(this.fitAddon);
    this.searchAddon = new SearchAddon();
    this.term.loadAddon(this.searchAddon);
    this.term.loadAddon(new WebLinksAddon(openLink));
    this.term.onResize(({ cols, rows }) => this.sendSize(cols, rows));
    this.term.onData((data) => this.handleInput(data));
    this.term.attachCustomKeyEventHandler(
      (event) => this.keyHandler?.(event) ?? true,
    );

    this.socket = new WebSocket(consoleWsUrl(pid));
    this.socket.onopen = () => this.handleOpen();
    this.socket.onclose = (event) => this.handleClose(event);
  }

  // Shows the terminal in host, the Console page.
  mount(host) {
    this.host = host;
    host.appendChild(this.element);

    // xterm measures its characters when it opens, so it opens once the
    // element is in the page
    if (!this.opened) {
      this.term.open(this.element);
      this.opened = true;
    }

    this.fit();
  }

  // Takes the terminal out of the page; a console that has ended goes too.
  unmount() {
    this.host = null;
    this.element.remove();

    if (this.state === 'ended' || this.state === 'lost') {
      this.dispose();
    }
  }

  fit() {
    if (this.host) {
      this.fitAddon.fit();
    }
  }

  // Calls listener(state) when the state changes; returns an unsubscribe.
  onChange(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  // Calls listener(line) with each line the user enters; returns an
  // unsubscribe.
  onSubmit(listener) {
    this.submitListeners.add(listener);
    return () => this.submitListeners.delete(listener);
  }

  // handler(event) sees each key first; returning false keeps it from the
  // console, and undefined or true lets the terminal have it.
  setKeyHandler(handler) {
    this.keyHandler = handler;
  }

  handleInput(data) {
    for (const line of this.lines.feed(data)) {
      const text = line ?? afterPrompt(this.cursorRow()) ?? '';
      this.submitListeners.forEach((listener) => listener(text));
    }
  }

  // the text of the row the cursor is on, with the rows it wraps from
  cursorRow() {
    const buffer = this.term.buffer.active;
    let y = buffer.baseY + buffer.cursorY;
    let text = buffer.getLine(y)?.translateToString(true) ?? '';
    while (y > 0 && buffer.getLine(y)?.isWrapped) {
      y--;
      text = (buffer.getLine(y)?.translateToString(true) ?? '') + text;
    }
    return text;
  }

  // Types text into the console as if the user had.
  type(text) {
    if (this.state === 'open') {
      this.term.input(text, true);
    }
    this.focus();
  }

  // the whole of the terminal's output, as text
  text() {
    return bufferText(this.term.buffer.active);
  }

  // the number of lines the output has once the terminal has dropped older
  // ones to keep its scrollback, else null
  keptLines() {
    const length = this.term.buffer.active.length;
    return length >= this.term.rows + SCROLLBACK ? length : null;
  }

  // the text selected in the terminal, or ''
  selection() {
    return this.term.hasSelection() ? this.term.getSelection() : '';
  }

  clear() {
    this.term.clear();
    this.focus();
  }

  // Scrolls the output n pages down, or up for a negative n.
  scrollPages(n) {
    this.term.scrollPages(n);
  }

  scrollToTop() {
    this.term.scrollToTop();
  }

  scrollToBottom() {
    this.term.scrollToBottom();
  }

  focus() {
    this.term.focus();
  }

  // Finds text after (or before) the selection, or from its start when
  // incremental; reports whether it did.
  find(text, { backwards = false, caseSensitive = false, incremental } = {}) {
    const options = { caseSensitive, incremental: Boolean(incremental) };
    return backwards
      ? this.searchAddon.findPrevious(text, options)
      : this.searchAddon.findNext(text, options);
  }

  clearSearch() {
    this.searchAddon.clearDecorations();
    this.term.clearSelection();
  }

  setTheme(dark) {
    this.term.options.theme = terminalTheme(dark);
  }

  setFontSize(size) {
    this.term.options.fontSize = clampFontSize(size);
    this.fit();
  }

  // Ends the console; its socket then closes and the session ends.
  async end() {
    try {
      await consoleApi.endConsole(this.pid);
    } catch (err) {
      // already ended
      if (err.response?.status !== 404) {
        throw err;
      }
    }
  }

  sendSize(cols, rows) {
    if (this.state === 'ended' || this.state === 'lost') {
      return;
    }

    consoleApi.resizeConsole(this.pid, cols, rows).catch((err) => {
      console.warn('failed to resize the console', err);
    });
  }

  handleOpen() {
    // the attach addon throws on input if it is loaded before the socket is
    // open
    this.attachAddon = new AttachAddon(this.socket);
    this.term.loadAddon(this.attachAddon);
    this.setState('open');
    this.fit();
    // a fit that keeps the size sends none, and the console may have another
    // tab's size
    if (this.host) {
      this.sendSize(this.term.cols, this.term.rows);
    }
  }

  async handleClose(event) {
    this.attachAddon?.dispose();
    this.attachAddon = null;

    const outcome = await this.closeOutcome(event);
    // the server ends its consoles as it shuts down
    if (outcome !== 'lost') {
      forgetConsolePid(this.username, this.pid);
    }
    // the page may have let the session go while the server was asked
    if (this.disposed) {
      return;
    }

    if (outcome === 'shutdown') {
      this.term.write('\r\n[console ended: phēnix server shutting down]\r\n');
      this.finish('ended');
    } else if (outcome === 'ended') {
      this.term.write('\r\n[console ended]\r\n');
      this.finish('ended');
    } else {
      this.term.write('\r\n[console connection lost]\r\n');
      this.finish('lost');
    }
  }

  // Why the socket closed: 'shutdown' (the server is shutting down), 'ended'
  // (the console ended), or 'lost' (only this connection to it).
  async closeOutcome(event) {
    if (wsCloseCause(event) === 'shutdown' || serverShuttingDown()) {
      return 'shutdown';
    }
    // a dropped socket may be the first sign of a shutdown
    if (event.code !== NORMAL_CLOSE) {
      return (await shutdownWithin()) ? 'shutdown' : 'lost';
    }
    try {
      if (await consoleRunning(this.pid)) return 'lost';
      return serverShuttingDown() ? 'shutdown' : 'ended';
    } catch (err) {
      if (gotNoResponse(err) && (await shutdownWithin())) return 'shutdown';
      console.warn('failed to check whether the console is running', err);
      return 'lost';
    }
  }

  finish(state) {
    // the next visit to the Console page opens a console again
    if (currentConsole() === this) {
      setCurrentConsole(null);
    }

    this.setState(state);

    if (!this.host) {
      this.dispose();
    }
  }

  setState(state) {
    this.state = state;
    this.listeners.forEach((listener) => listener(state));
  }

  dispose() {
    if (this.disposed) {
      return;
    }
    this.disposed = true;
    this.listeners.clear();
    this.submitListeners.clear();
    this.keyHandler = null;

    this.socket.onopen = null;
    this.socket.onclose = null;
    this.socket.close();

    this.attachAddon?.dispose();
    this.attachAddon = null;
    this.term.dispose();
    this.element.remove();

    if (currentConsole() === this) {
      setCurrentConsole(null);
    }
  }
}

// Opens a link in the console's output in a new tab that cannot reach this
// page.
function openLink(event, uri) {
  event?.preventDefault?.();
  window.open(uri, '_blank', 'noopener,noreferrer');
}
