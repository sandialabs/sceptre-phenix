// The minimega console this tab has open, kept outside the Console page so
// leaving and coming back keeps it; its terminal is in consoleTerminal.js. Its
// pid is remembered per user, so a reload or another tab attaches to it; the
// server ends it on exit, on logout, or after 15 seconds open in no tab.
import * as consoleApi from '@/api/console.js';
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
    await consoleApi.endConsole(pid);
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
