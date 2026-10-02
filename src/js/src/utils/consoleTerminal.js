// The terminal and websocket of a minimega console. A session outlives the
// Console page that shows it (see consoleSession.js): the page mounts its
// terminal when it opens and unmounts it when it closes, while the websocket
// stays open and the terminal keeps receiving output.
import { markRaw, ref } from 'vue';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { AttachAddon } from '@xterm/addon-attach';

import '@xterm/xterm/css/xterm.css';

import axiosInstance from '@/utils/axios.js';
import { usePhenixStore } from '@/store.js';
import {
  currentConsole,
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

let opening = null;

// Resolves to this tab's console session: the one it has open, else the one
// this browser had open if it is still running, else a new console.
export function openConsole() {
  const session = currentConsole();
  if (session) {
    return Promise.resolve(session);
  }

  opening ??= findOrStartConsole().finally(() => {
    opening = null;
    consolePhase.value = null;
  });
  return opening;
}

async function findOrStartConsole() {
  const username = usePhenixStore().username;
  let pid = rememberedConsolePid(username);

  if (pid !== null) {
    consolePhase.value = 'reconnecting';
    if (!(await consoleRunning(pid))) {
      forgetConsolePid(username, pid);
      pid = null;
    }
  }

  if (pid === null) {
    consolePhase.value = 'starting';
    pid = (await axiosInstance.post('console')).data.pid;
    rememberConsolePid(username, pid);
  }

  const session = markRaw(new ConsoleSession(pid, username));
  setCurrentConsole(session);
  return session;
}

async function consoleRunning(pid) {
  try {
    await axiosInstance.get(`console/${pid}`);
    return true;
  } catch (err) {
    if (err.response?.status === 404) {
      return false;
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
  constructor(pid, username) {
    this.pid = pid;
    this.username = username;
    // 'connecting', 'open', 'ended' (the console exited or was ended), or
    // 'lost' (the connection to it failed; the console may still be running)
    this.state = 'connecting';
    this.host = null;
    this.listeners = new Set();
    this.opened = false;
    this.disposed = false;
    this.attachAddon = null;

    // the terminal renders into this element, which moves between visits
    this.element = document.createElement('div');
    this.element.className = 'console-terminal';

    this.term = new Terminal();
    this.fitAddon = new FitAddon();
    this.term.loadAddon(this.fitAddon);
    this.term.onResize(({ cols, rows }) => this.sendSize(cols, rows));

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

  sendSize(cols, rows) {
    if (this.state === 'ended' || this.state === 'lost') {
      return;
    }

    axiosInstance
      .post(`console/${this.pid}/size?cols=${cols}&rows=${rows}`)
      .catch((err) => {
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
  }

  async handleClose(event) {
    this.attachAddon?.dispose();
    this.attachAddon = null;

    const ended = await this.consoleEnded(event);
    if (ended) {
      forgetConsolePid(this.username, this.pid);
    }
    // the page may have let the session go while the server was asked
    if (this.disposed) {
      return;
    }

    if (ended) {
      this.term.write('\r\n[console ended]\r\n');
      this.finish('ended');
    } else {
      this.term.write('\r\n[console connection lost]\r\n');
      this.finish('lost');
    }
  }

  // whether the socket closed because the console ended, rather than only
  // this connection to it
  async consoleEnded(event) {
    if (event.code !== NORMAL_CLOSE) {
      return false;
    }
    try {
      return !(await consoleRunning(this.pid));
    } catch (err) {
      console.warn('failed to check whether the console is running', err);
      return false;
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
