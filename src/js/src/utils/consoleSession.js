// The minimega console this tab has open. It is kept here rather than in the
// Console page, so moving to another page and back keeps the console and its
// output. The terminal itself is built in consoleTerminal.js, which loads with
// the Console page; this module stays small because logging out needs it.
//
// The console's pid is also remembered per user in localStorage, so a reload
// or another tab attaches to the same console. The server keeps a console
// until its process exits (CTRL+D or `disconnect`), its user ends it (on
// logout), or no tab has had it open for 15 seconds.
import axiosInstance from '@/utils/axios.js';
import { readPref, removePref, writePref } from '@/utils/prefs.js';
import { usePhenixStore } from '@/store.js';

let current = null;
let watchingLogout = false;

const pidKey = (username) => `phenix.console.${username ?? ''}`;

export function currentConsole() {
  return current;
}

export function setCurrentConsole(session) {
  current = session;
  if (session) {
    watchLogout();
  }
}

export function rememberedConsolePid(username) {
  const pid = Number(readPref(pidKey(username)));
  return Number.isInteger(pid) && pid > 0 ? pid : null;
}

// without storage a reload starts a new console
export function rememberConsolePid(username, pid) {
  writePref(pidKey(username), pid);
}

// Forgets the remembered console; given a pid, only if that is still the one
// remembered, since another tab may have started a newer console since.
export function forgetConsolePid(username, pid) {
  if (pid === undefined || rememberedConsolePid(username) === pid) {
    removePref(pidKey(username));
  }
}

// Lets go of this tab's console without ending it: the server ends it once no
// tab has it open.
export function closeConsole() {
  const session = current;
  current = null;
  session?.dispose();
}

// Ends the user's console, as logging out does. Call it before the logout
// request, while the token is still valid.
export async function endConsole() {
  const username = usePhenixStore().username;
  const pid = current?.pid ?? rememberedConsolePid(username);

  closeConsole();
  forgetConsolePid(username);

  if (!pid) {
    return;
  }

  try {
    await axiosInstance.delete(`console/${pid}`);
  } catch (err) {
    // already ended
    if (err.response?.status !== 404) {
      console.warn('failed to end the console', err);
    }
  }
}

// The store also logs out when the token has expired or was refused, with no
// chance to end the console first; let go of it then, so it is not left
// attached to a signed-out tab.
function watchLogout() {
  if (watchingLogout) {
    return;
  }
  watchingLogout = true;

  usePhenixStore().$onAction(({ name, store }) => {
    if (name === 'logout') {
      const pid = current?.pid;
      closeConsole();
      if (pid) {
        forgetConsolePid(store.username, pid);
      }
    }
  }, true);
}
