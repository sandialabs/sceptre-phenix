// Logging out while Builder holds changes the server does not have.
//
// A logout deletes the data that Builder keeps in this browser (see
// builder/session.js). This data includes edits that still wait for the
// server: edits made offline, refused, in conflict, or made after the
// session ended. So every logout first looks for these edits:
//
// - A logout that the user asks for sends them first. When some remain, it
//   asks the user and waits for an answer.
// - A logout that the app starts (the idle timeout, an expired or invalid
//   token) shows the same warning for a minute, then logs out. The tab's
//   title also counts down, so a hidden tab shows the countdown.
// - Without such edits, the logout continues immediately, as before.
//
// On the Builder page, the warning for an expired session also lets the
// user sign in again on that page. This keeps the changes and sends them
// (see builder/signin.js). A logout that the user asked for does not offer
// this.

// How long an automatic logout waits, in seconds, and the seconds left when
// the warning announces the countdown again.
export const LOGOUT_COUNTDOWN_S = 60;
export const LOGOUT_NOTICE_S = 10;

// Why the app logs out: the user asked ('manual'), the idle timeout
// ('idle'), or the token expired or was refused ('expired'). A later reason
// in this list outranks an earlier one. An idle logout ends a question that
// the user left open. An expired token leaves no way to stay.
const REASONS = ['manual', 'idle', 'expired'];

function seconds(n) {
  return `${n} second${n === 1 ? '' : 's'}`;
}

/**
 * The countdown line of an automatic logout.
 *
 * @param {number} secondsLeft
 * @returns {string}
 */
export function countdownText(secondsLeft) {
  return `Logging out in ${seconds(secondsLeft)}.`;
}

const TITLE_COUNTDOWN = /^Logging out in \d+ seconds? – /;

/**
 * The page title while an automatic logout counts down. A hidden tab shows
 * this title too. It is the countdown before the title, or the title alone.
 *
 * @param {string} title the current title, with or without the countdown
 * @param {number|null} secondsLeft null when nothing counts down
 * @returns {string}
 */
export function countdownTitle(title, secondsLeft) {
  const base = title.replace(TITLE_COUNTDOWN, '');

  return typeof secondsLeft === 'number'
    ? `Logging out in ${seconds(secondsLeft)} – ${base}`
    : base;
}

/**
 * What the warning says.
 *
 * @param {object} warning reason, changes (not sent), unapplied (what the
 *   Inspector cannot apply, as a sentence, or ''), drafts (those Download can
 *   save), canStay, canSignIn (sign in again without leaving the page),
 *   secondsLeft (null without a countdown)
 * @returns {{title: string, message: string, confirm: string, stay: string,
 *   signIn: string}}
 */
export function logoutWarningText({
  reason,
  changes,
  unapplied,
  drafts = [],
  canStay,
  canSignIn = false,
  secondsLeft = null,
}) {
  const one = changes === 1;
  const lead = {
    manual: '',
    idle: 'You have been inactive for a while.',
    expired: canSignIn ? '' : 'To sign in again, you must log out.',
  }[reason];
  const unsent = changes
    ? `${changes} change${one ? '' : 's'} to Builder drafts ${one ? 'has' : 'have'} not reached the server.`
    : '';
  const resume =
    canSignIn && changes ? `Sign in again to save ${one ? 'it' : 'them'}.` : '';
  const lost =
    changes === 1 && !unapplied
      ? 'Logging out deletes it from this browser.'
      : changes
        ? 'Logging out deletes them from this browser.'
        : 'Logging out loses them.';
  // Download saves the drafts the server lacks, not the Inspector's edits.
  const keep =
    drafts.length > 0
      ? 'Use Download to keep a copy.'
      : unapplied && canStay
        ? 'Stay signed in to fix them.'
        : unapplied && canSignIn
          ? 'Sign in again to fix them.'
          : '';

  return {
    title: {
      manual: 'Log out with unsaved changes?',
      idle: 'You will be logged out',
      expired: 'Your session has expired',
    }[reason],
    message: [lead, unsent, resume, unapplied, lost, keep]
      .filter(Boolean)
      .join(' '),
    confirm: typeof secondsLeft === 'number' ? 'Log out now' : 'Log out anyway',
    stay: 'Stay signed in',
    signIn: 'Sign in again',
  };
}

// Whether the page is hidden, and a way to watch for changes to it.
const pageVisibility = {
  hidden: () => globalThis.document?.visibilityState === 'hidden',
  watch(callback) {
    globalThis.document?.addEventListener('visibilitychange', callback);
  },
};

/**
 * Creates the logout flow (see the header).
 *
 * @param {object} options
 * @param {(options: {send: boolean}) => Promise<{changes: number,
 *   unapplied: string}>} options.findUnsent what the server does not have,
 *   after sending it when `send`
 * @param {(reason: string) => Promise<boolean>} options.finish logs out.
 *   Resolves to whether it did
 * @param {(warning: object|null) => void} options.show shows the warning, a
 *   new object on every change, or null to close it
 * @param {(busy: boolean) => void} [options.busy] whether a logout is in
 *   progress
 * @param {() => boolean} [options.canSignIn] whether the user can sign in
 *   again without leaving the page (see builder/signin.js)
 * @param {() => void} [options.signIn] asks for the password again on the
 *   page
 * @param {() => number} [options.now]
 * @param {object} [options.timers] setTimeout, clearTimeout, setInterval,
 *   clearInterval
 * @param {{hidden: () => boolean, watch: (callback: () => void) => void}}
 *   [options.visibility] the page's visibility
 * @returns {{request: Function, answer: Function}}
 */
export function createLogoutFlow({
  findUnsent,
  finish,
  show,
  busy = () => {},
  canSignIn = () => false,
  signIn = () => {},
  now = () => Date.now(),
  timers = globalThis,
  visibility = pageVisibility,
}) {
  let current = null;

  function publish() {
    show(current?.warning ? { ...current.warning } : null);
  }

  function stopCountdown() {
    timers.clearTimeout(current?.deadlineTimer);
    timers.clearInterval(current?.tick);

    if (current) {
      current.deadline = null;
    }
  }

  // For an expired session, the countdown stops while the page is hidden,
  // so the user sees the warning for the full minute. The server refuses
  // the token during that time anyway. The idle timeout's countdown does
  // not stop: a session that the user leaves alone ends on time.
  function held() {
    return current.reason === 'expired' && visibility.hidden();
  }

  // Counts down the `ms` left. It reads the seconds left from the clock
  // instead of counting them, because a hidden tab runs its timers late.
  function runCountdown(ms) {
    const deadline = now() + ms;
    const warning = current.warning;

    current.deadline = deadline;
    current.deadlineTimer = timers.setTimeout(() => answer('logout'), ms);
    current.tick = timers.setInterval(() => {
      const left = Math.max(0, Math.ceil((deadline - now()) / 1000));

      if (left === 0) {
        answer('logout');

        return;
      }

      if (left === warning.secondsLeft) {
        return;
      }

      warning.secondsLeft = left;

      // Announces the countdown once more near the end, not every second.
      if (left <= LOGOUT_NOTICE_S && !warning.notice) {
        warning.notice = countdownText(left);
      }

      publish();
    }, 1000);
  }

  // Stops or continues the countdown when the page hides or shows (see
  // held).
  function followVisibility() {
    if (typeof current?.warning?.secondsLeft !== 'number') {
      return;
    }

    if (!held()) {
      if (!current.deadline) {
        runCountdown(current.left);
      }
    } else if (current.deadline) {
      current.left = Math.max(0, current.deadline - now());
      stopCountdown();
    }
  }

  visibility.watch(followVisibility);

  function startCountdown() {
    current.left = LOGOUT_COUNTDOWN_S * 1000;
    current.warning.secondsLeft = LOGOUT_COUNTDOWN_S;
    followVisibility();
  }

  // An automatic logout of an expired session offers to sign in again when
  // the page can do that (see the header).
  function offersSignIn() {
    return current.reason === 'expired' && !current.manual && canSignIn();
  }

  function ask(unsent) {
    return new Promise((resolve) => {
      current.resolve = resolve;
      current.warning = {
        reason: current.reason,
        changes: unsent.changes || 0,
        unapplied: unsent.unapplied || '',
        drafts: unsent.drafts || [],
        canStay: current.reason !== 'expired',
        canSignIn: offersSignIn(),
        secondsLeft: null,
        notice: '',
      };

      if (current.countdown) {
        startCountdown();
      }

      publish();
    });
  }

  async function run(flow) {
    busy(true);

    try {
      const unsent = await findUnsent({
        send: flow.reason !== 'expired',
      }).catch(() => ({ changes: 0, unapplied: '' }));

      if (unsent.changes > 0 || unsent.unapplied) {
        const choice = await ask(unsent);

        // Signing in again keeps the session too, once this logout ends.
        if (choice === 'signin') {
          flow.signIn = true;
        }

        if (choice !== 'logout') {
          return 'stayed';
        }
      }

      return (await finish(flow.reason).catch(() => false))
        ? 'logged-out'
        : 'failed';
    } finally {
      busy(false);
    }
  }

  // Applies a later, stronger reason while a logout is in progress (see
  // REASONS).
  function escalate(reason, countdown) {
    if (REASONS.indexOf(reason) <= REASONS.indexOf(current.reason)) {
      return;
    }

    current.reason = reason;
    current.countdown ||= countdown;

    const warning = current.warning;

    if (!warning) {
      return;
    }

    warning.reason = reason;
    warning.canStay = reason !== 'expired';
    warning.canSignIn = offersSignIn();

    if (current.countdown && warning.secondsLeft === null) {
      startCountdown();
    } else {
      followVisibility();
    }

    publish();
  }

  /**
   * Answers the warning.
   *
   * @param {'stay'|'signin'|'logout'} choice ignores stay when the session
   *   cannot continue, and signin when the warning does not offer it
   */
  function answer(choice) {
    if (
      !current?.resolve ||
      (choice === 'stay' && !current.warning.canStay) ||
      (choice === 'signin' && !current.warning.canSignIn)
    ) {
      return;
    }

    const resolve = current.resolve;

    stopCountdown();
    current.resolve = null;
    current.warning = null;
    publish();
    resolve(choice);
  }

  return {
    /**
     * Logs out, or warns first (see the header). A request during a logout
     * in progress joins that logout.
     *
     * @param {'manual'|'idle'|'expired'} [reason]
     * @param {object} [options] countdown: whether the warning ends by
     *   itself after a minute. The default is true unless the reason is
     *   'manual'
     * @returns {Promise<'logged-out'|'stayed'|'failed'>} also 'stayed' when
     *   the user chose to sign in again. The sign-in then opens
     */
    request(reason = 'manual', { countdown = reason !== 'manual' } = {}) {
      if (current) {
        escalate(reason, countdown);

        return current.promise;
      }

      // A logout that the user asked for stays manual after a stronger
      // reason.
      const flow = { reason, countdown, manual: !countdown, signIn: false };

      current = flow;
      flow.promise = run(flow).finally(() => {
        if (current === flow) {
          current = null;
        }

        if (flow.signIn) {
          signIn();
        }
      });

      return flow.promise;
    },

    answer,
  };
}
