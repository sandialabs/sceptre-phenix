// Signing in again without leaving Builder.
//
// A session can end while the Builder is open: its token expires, or the
// server no longer knows it (a request answered 401). Logout would delete
// what the Builder keeps in this browser, including the edits the server
// does not have (see session.js). The sign-in page is not available without
// logout. So the Builder asks for the password again in place, as the same
// user (see BuilderSignIn.vue). The save queues wait and keep their edits.
// They send the edits after the user signs in. Nothing here clears
// anything.
//
// The Builder view hosts the dialog while it is open. The session ends as
// before (see utils/logout.js) in these cases: there is no host, sign-in
// takes no password (proxy authentication), or a logout is in progress.
//
// The app loads this module (the router and the app store read it), so the
// module stays small and imports neither.

import axios from 'axios';
import { reactive } from 'vue';

export const signIn = reactive({
  // Whether the dialog is open.
  open: false,
  // Whether the session has ended and the user has not signed in again:
  // a request was refused (401), or the token expired.
  needed: false,
  // Whether the user closed the dialog without signing in. After that, a
  // refused request or an expired token does not open it again. The
  // Builder offers Sign in again instead.
  declined: false,
});

// The Builder view, while it is open (see hostSignIn).
let host = null;

function reset() {
  signIn.open = false;
  signIn.needed = false;
  signIn.declined = false;
}

/**
 * Hosts the dialog: the Builder view, while it is open.
 *
 * @param {object} options available: () => whether this session can sign
 *   in again with a password. busy: () => whether a logout is in progress
 * @returns {() => void} ends it, closing the dialog
 */
export function hostSignIn(options) {
  host = options;

  return () => {
    if (host === options) {
      host = null;
      reset();
    }
  };
}

/** @returns {boolean} whether the Builder can ask for the password again */
export function signInAvailable() {
  return Boolean(host?.available());
}

/**
 * The session has ended while the Builder is open: a request was refused
 * (401) or the token expired. The dialog opens, unless the user declined
 * it or a logout is in progress.
 *
 * @returns {boolean} whether it is open
 */
export function sessionEnded() {
  if (!signInAvailable() || host.busy?.()) {
    return false;
  }

  signIn.needed = true;

  if (!signIn.declined) {
    signIn.open = true;
  }

  return signIn.open;
}

/**
 * Opens the dialog because the user asked: Retry saving, the Builder's
 * Sign in again, or the logout warning's.
 *
 * @returns {boolean} whether it is open
 */
export function requestSignIn() {
  if (!signInAvailable() || host.busy?.()) {
    return false;
  }

  signIn.needed = true;
  signIn.declined = false;
  signIn.open = true;

  return true;
}

/** Closes the dialog without signing in: the edits stay queued. */
export function declineSignIn() {
  signIn.open = false;
  signIn.declined = true;
}

/** The user signed in again: the dialog closes, and nothing is needed. */
export function signedIn() {
  reset();
}

// Signs in with the plain client. The app's own client sends the ended
// session's token, which the server refuses before it reads the password.
function postLogin(credentials) {
  return axios.post(`${import.meta.env.BASE_URL}api/v1/login`, credentials);
}

/**
 * Signs in again as `username`: takes the new token (renew) and sends
 * what the server refused while the session was over (resume).
 *
 * @param {object} options username, password. renew: (loginResponse) =>
 *   whether the token was taken. resume: () => what signing in sends.
 *   post: for tests
 * @returns {Promise<*>} resume's result
 */
export async function signInAgain({
  username,
  password,
  renew,
  resume,
  post = postLogin,
}) {
  const response = await post({ user: username, pass: password });

  if (!renew(response?.data)) {
    throw Object.assign(new Error('signed in as another user'), {
      anotherUser: true,
    });
  }

  signedIn();

  return resume();
}

/**
 * What the dialog says when signing in again fails.
 *
 * @param {object} error axios-like error, or signInAgain's
 * @returns {string}
 */
export function signInError(error) {
  const status = error?.response?.status;

  if (status === 401) {
    return 'The password is incorrect.';
  }

  if (status === 404) {
    return 'This user no longer exists. To sign in as someone else, log out.';
  }

  // The server takes no passwords (proxy authentication).
  if (status === 400) {
    return 'This server does not take a password here. Use Download to keep a copy of your changes, then log out and sign in again.';
  }

  if (!error?.response && !error?.anotherUser) {
    return 'The server could not be reached. Check the connection and try again.';
  }

  return 'Could not sign in. Try again.';
}

/**
 * When a token expires, from its claims.
 *
 * @param {string|null} token a JWT
 * @returns {number|null} milliseconds since the epoch, or null for a token
 *   that is no JWT or names no expiry
 */
export function tokenExpiry(token) {
  try {
    const claims = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const { exp } = JSON.parse(atob(claims));

    return Number.isFinite(exp) ? exp * 1000 : null;
  } catch {
    return null;
  }
}

/**
 * What the router does with a navigation made while the dialog is open,
 * or after the token expired (see router.js):
 * - While the dialog is open, the page stays for it, and nothing logs out.
 * - A navigation within the Builder, which only changes its address,
 *   continues, and the session end opens the dialog.
 * - Any other navigation with an expired token logs out. It shows the
 *   warning first when the Builder holds changes the server does not have
 *   (see utils/logout.js).
 *
 * @param {object} to the route navigated to
 * @param {object} from the route navigated from
 * @returns {'go'|'stay'|'logout'}
 */
export function expiredNavigation(to, from) {
  const within = Boolean(to?.name) && to.name === from?.name;

  if (signIn.open) {
    return within ? 'go' : 'stay';
  }

  if (within && to.name === 'builder' && signInAvailable()) {
    sessionEnded();

    return 'go';
  }

  return 'logout';
}
