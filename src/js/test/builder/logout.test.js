// Logging out while Builder holds changes the server does not have:
// when to warn and for how long, what the warning says, and how the changes
// queued in this browser are found, sent and downloaded, with the Builder
// open or not.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h, reactive } from 'vue';
import { renderToString } from 'vue/server-renderer';

const phenix = vi.hoisted(() => ({ state: null }));

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix.state }));

const api = vi.hoisted(() => ({ appendSnapshot: vi.fn() }));

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: api };
});

import { createPinia } from 'pinia';

import LogoutWarning from '@/components/LogoutWarning.vue';
import BuilderSignIn from '@/components/builder/BuilderSignIn.vue';
import { createMemoryStore, draftKey } from '@/builder/idb.js';
import {
  LOGOUT_SEND_WAIT_MS,
  diagramFile,
  draftExport,
  registerOpenDraft,
  registerQueue,
  unsentBuilderWork,
} from '@/builder/session.js';
import { hostSignIn, signIn } from '@/builder/signin.js';
import { ANSWER_WAIT_MS, answerPresence, openChannel } from '@/builder/tabs.js';
import {
  LOGOUT_COUNTDOWN_S,
  LOGOUT_NOTICE_S,
  countdownText,
  countdownTitle,
  createLogoutFlow,
  logoutWarningText,
} from '@/utils/logout.js';

afterEach(() => {
  vi.useRealTimers();
});

// A logout flow whose search finds `unsent`, recording what it shows, on a
// page that is hidden while `page.hidden`; page.change() says it changed.
// With `canSignIn`, the page can sign in again (the Builder's).
function setup({
  unsent = { changes: 0, unapplied: '' },
  finished = true,
  canSignIn = false,
} = {}) {
  const shown = [];
  const findUnsent = vi.fn(async () => unsent);
  const finish = vi.fn(async () => finished);
  const busy = vi.fn();
  const signIn = vi.fn();
  const page = { hidden: false, change: () => {} };
  const flow = createLogoutFlow({
    findUnsent,
    finish,
    busy,
    canSignIn: () => canSignIn,
    signIn,
    show: (warning) => shown.push(warning),
    visibility: {
      hidden: () => page.hidden,
      watch(callback) {
        page.change = callback;
      },
    },
  });

  return {
    flow,
    findUnsent,
    finish,
    busy,
    signIn,
    shown,
    page,
    last: () => shown.at(-1),
  };
}

// Settles what the flow awaits, and runs the timers due in `ms`.
const pass = (ms = 0) => vi.advanceTimersByTimeAsync(ms);

describe('logging out', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  test('goes ahead at once when nothing is unsent, sending first unless the token expired', async () => {
    const { flow, findUnsent, finish, busy, shown } = setup();

    await expect(flow.request('manual')).resolves.toBe('logged-out');
    expect(findUnsent).toHaveBeenLastCalledWith({ send: true });
    expect(finish).toHaveBeenLastCalledWith('manual');

    await expect(flow.request('idle')).resolves.toBe('logged-out');
    expect(findUnsent).toHaveBeenLastCalledWith({ send: true });

    await expect(flow.request('expired')).resolves.toBe('logged-out');
    expect(findUnsent).toHaveBeenLastCalledWith({ send: false });
    expect(finish).toHaveBeenLastCalledWith('expired');

    expect(shown).toEqual([]);
    expect(busy.mock.calls.flat()).toEqual([
      true,
      false,
      true,
      false,
      true,
      false,
    ]);
  });

  test('a logout the user asked for warns, and waits for an answer however long it takes', async () => {
    const { flow, finish, last } = setup({
      unsent: { changes: 2, unapplied: '' },
    });
    const outcome = flow.request();

    await pass();
    expect(last()).toEqual({
      reason: 'manual',
      changes: 2,
      unapplied: '',
      drafts: [],
      canStay: true,
      canSignIn: false,
      secondsLeft: null,
      notice: '',
    });

    await pass(10 * 60 * 1000);
    expect(last()).not.toBeNull();
    expect(finish).not.toHaveBeenCalled();

    flow.answer('stay');
    await expect(outcome).resolves.toBe('stayed');
    expect(last()).toBeNull();
    expect(finish).not.toHaveBeenCalled();
  });

  test('Log out anyway logs out, and says when the logout failed', async () => {
    let { flow, finish, last } = setup({ unsent: { changes: 1 } });
    let outcome = flow.request('manual');

    await pass();
    flow.answer('logout');
    expect(last()).toBeNull();
    await expect(outcome).resolves.toBe('logged-out');
    expect(finish).toHaveBeenCalledWith('manual');

    ({ flow } = setup({ unsent: { changes: 1 }, finished: false }));
    outcome = flow.request('manual');
    await pass();
    flow.answer('logout');
    await expect(outcome).resolves.toBe('failed');

    ({ flow, finish } = setup({ unsent: { changes: 1 } }));
    finish.mockRejectedValue(new Error('offline'));
    outcome = flow.request('manual');
    await pass();
    flow.answer('logout');
    await expect(outcome).resolves.toBe('failed');
  });

  test('an idle logout counts down a minute, says so once more near the end, then logs out', async () => {
    const { flow, finish, last, shown } = setup({ unsent: { changes: 1 } });
    const outcome = flow.request('idle');

    await pass();
    expect(last()).toMatchObject({
      reason: 'idle',
      canStay: true,
      secondsLeft: LOGOUT_COUNTDOWN_S,
      notice: '',
    });

    await pass(1000);
    expect(last().secondsLeft).toBe(59);

    await pass((LOGOUT_COUNTDOWN_S - LOGOUT_NOTICE_S - 2) * 1000);
    expect(last()).toMatchObject({ secondsLeft: 11, notice: '' });

    await pass(1000);
    expect(last()).toMatchObject({
      secondsLeft: 10,
      notice: 'Logging out in 10 seconds.',
    });

    // The notice is not replaced as the seconds go on.
    await pass(5000);
    expect(last()).toMatchObject({
      secondsLeft: 5,
      notice: 'Logging out in 10 seconds.',
    });
    expect(new Set(shown.map((warning) => warning.notice))).toEqual(
      new Set(['', 'Logging out in 10 seconds.']),
    );
    expect(finish).not.toHaveBeenCalled();

    await pass(5000);
    expect(finish).toHaveBeenCalledWith('idle');
    expect(last()).toBeNull();
    await expect(outcome).resolves.toBe('logged-out');
  });

  test('Stay signed in stops the countdown', async () => {
    const { flow, finish, shown } = setup({ unsent: { changes: 1 } });
    const outcome = flow.request('idle');

    await pass(30000);
    flow.answer('stay');
    await expect(outcome).resolves.toBe('stayed');

    const count = shown.length;

    await pass(120000);
    expect(finish).not.toHaveBeenCalled();
    expect(shown).toHaveLength(count);
  });

  test('an expired session cannot stay signed in', async () => {
    const { flow, finish, last } = setup({ unsent: { changes: 1 } });
    const outcome = flow.request('expired');

    await pass();
    expect(last()).toMatchObject({ reason: 'expired', canStay: false });

    flow.answer('stay');
    expect(last()).not.toBeNull();

    await pass(LOGOUT_COUNTDOWN_S * 1000);
    expect(finish).toHaveBeenCalledWith('expired');
    await expect(outcome).resolves.toBe('logged-out');
  });

  test('a logout the user asked for once the session expired sends nothing, offers no Stay and waits', async () => {
    const { flow, findUnsent, finish, last } = setup({
      unsent: { changes: 1 },
    });
    const outcome = flow.request('expired', { countdown: false });

    await pass();
    expect(findUnsent).toHaveBeenCalledWith({ send: false });
    expect(last()).toMatchObject({
      reason: 'expired',
      canStay: false,
      secondsLeft: null,
    });

    // Neither time nor a later expiry ends it by itself.
    await pass(10 * 60 * 1000);
    expect(flow.request('expired')).toBe(outcome);
    await pass(2 * LOGOUT_COUNTDOWN_S * 1000);
    expect(last()).toMatchObject({ secondsLeft: null });
    expect(finish).not.toHaveBeenCalled();

    flow.answer('stay');
    expect(last()).not.toBeNull();
    flow.answer('logout');
    await expect(outcome).resolves.toBe('logged-out');
    expect(finish).toHaveBeenCalledWith('expired');
  });

  test("on the Builder's page, an expired session's warning offers to sign in again there: it stops the countdown, and the sign-in opens once the logout has ended", async () => {
    const { flow, finish, busy, signIn, last } = setup({
      unsent: { changes: 1 },
      canSignIn: true,
    });
    const outcome = flow.request('expired');

    await pass();
    expect(last()).toMatchObject({
      reason: 'expired',
      canStay: false,
      canSignIn: true,
      secondsLeft: LOGOUT_COUNTDOWN_S,
    });

    await pass(30000);
    flow.answer('signin');
    await expect(outcome).resolves.toBe('stayed');
    expect(last()).toBeNull();
    expect(signIn).toHaveBeenCalledOnce();
    // Opened once nothing logs out any more.
    expect(busy.mock.invocationCallOrder.at(-1)).toBeLessThan(
      signIn.mock.invocationCallOrder[0],
    );

    await pass(2 * LOGOUT_COUNTDOWN_S * 1000);
    expect(finish).not.toHaveBeenCalled();
  });

  test('only an automatic logout of an expired session offers to sign in again', async () => {
    // The idle timeout's, until the session expires meanwhile.
    let { flow, signIn, last } = setup({
      unsent: { changes: 1 },
      canSignIn: true,
    });
    const idle = flow.request('idle');

    await pass();
    expect(last()).toMatchObject({ canStay: true, canSignIn: false });
    flow.answer('signin');
    expect(last()).not.toBeNull();

    flow.request('expired');
    expect(last()).toMatchObject({ canStay: false, canSignIn: true });
    flow.answer('signin');
    await expect(idle).resolves.toBe('stayed');
    expect(signIn).toHaveBeenCalledOnce();

    // A logout the user asked for stays one, as does any logout on a page
    // that cannot sign in again.
    for (const options of [
      { canSignIn: true, request: ['expired', { countdown: false }] },
      { canSignIn: false, request: ['expired'] },
    ]) {
      ({ flow, signIn, last } = setup({
        unsent: { changes: 1 },
        canSignIn: options.canSignIn,
      }));

      const outcome = flow.request(...options.request);

      await pass();
      expect(last()).toMatchObject({ canSignIn: false });
      flow.answer('signin');
      expect(last()).not.toBeNull();
      flow.answer('logout');
      await expect(outcome).resolves.toBe('logged-out');
      expect(signIn).not.toHaveBeenCalled();
    }
  });

  test("an expired session's minute waits while the page is hidden; the idle timeout's does not", async () => {
    let { flow, finish, last, page } = setup({ unsent: { changes: 1 } });

    page.hidden = true;
    flow.request('expired');
    await pass(5 * 60 * 1000);
    expect(last()).toMatchObject({ secondsLeft: LOGOUT_COUNTDOWN_S });
    expect(finish).not.toHaveBeenCalled();

    page.hidden = false;
    page.change();
    await pass(20000);
    expect(last().secondsLeft).toBe(40);

    // Hidden again, it keeps the time left.
    page.hidden = true;
    page.change();
    await pass(5 * 60 * 1000);
    expect(finish).not.toHaveBeenCalled();
    page.hidden = false;
    page.change();
    await pass(39000);
    expect(finish).not.toHaveBeenCalled();
    await pass(1000);
    expect(finish).toHaveBeenCalledWith('expired');

    ({ flow, finish, page } = setup({ unsent: { changes: 1 } }));
    page.hidden = true;
    flow.request('idle');
    await pass(LOGOUT_COUNTDOWN_S * 1000);
    expect(finish).toHaveBeenCalledWith('idle');
  });

  test('the countdown follows the clock when the timers run late', async () => {
    const { flow, finish, last } = setup({ unsent: { changes: 1 } });

    flow.request('idle');
    await pass();

    // A hidden tab: the clock moves on, the timers do not.
    vi.setSystemTime(Date.now() + 45000);
    await pass(1000);
    expect(last().secondsLeft).toBe(14);

    vi.setSystemTime(Date.now() + 30000);
    await pass(1000);
    expect(finish).toHaveBeenCalledWith('idle');
  });

  test('an automatic logout takes over a warning that waits for an answer', async () => {
    const { flow, finish, findUnsent, last } = setup({
      unsent: { changes: 1 },
    });
    const outcome = flow.request('manual');

    await pass(5 * 60 * 1000);
    expect(flow.request('idle')).toBe(outcome);
    expect(last()).toMatchObject({
      reason: 'idle',
      canStay: true,
      secondsLeft: LOGOUT_COUNTDOWN_S,
    });

    // A token that expires meanwhile keeps the time left, but not Stay.
    await pass(10000);
    flow.request('expired');
    expect(last()).toMatchObject({
      reason: 'expired',
      canStay: false,
      secondsLeft: 50,
    });

    // A weaker reason changes nothing.
    flow.request('manual');
    expect(last().reason).toBe('expired');

    await pass(50000);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith('expired');
    expect(findUnsent).toHaveBeenCalledTimes(1);
    await expect(outcome).resolves.toBe('logged-out');
  });

  test('a reason that comes while it looks for changes is kept', async () => {
    let found;
    const { flow, findUnsent, last } = setup();

    findUnsent.mockReturnValue(
      new Promise((resolve) => {
        found = resolve;
      }),
    );
    flow.request('manual');
    flow.request('expired');
    found({ changes: 3, unapplied: '' });
    await pass();

    expect(findUnsent).toHaveBeenCalledWith({ send: true });
    expect(last()).toMatchObject({
      reason: 'expired',
      changes: 3,
      secondsLeft: LOGOUT_COUNTDOWN_S,
    });
  });

  test('a logout after one that ended looks for changes again', async () => {
    const { flow, findUnsent } = setup({ unsent: { changes: 1 } });
    const first = flow.request('manual');

    await pass();
    flow.answer('stay');
    await first;

    flow.request('manual');
    await pass();
    expect(findUnsent).toHaveBeenCalledTimes(2);
  });
});

describe('the warning', () => {
  test('names what is lost and the choices, plainly', () => {
    const drafts = [{ key: 'k1', name: 'lab.json' }];

    expect(
      logoutWarningText({
        reason: 'manual',
        changes: 1,
        unapplied: '',
        drafts,
        canStay: true,
        secondsLeft: null,
      }),
    ).toEqual({
      title: 'Log out with unsaved changes?',
      message:
        '1 change to Builder drafts has not reached the server. Logging out deletes it from this browser. Use Download to keep a copy.',
      confirm: 'Log out anyway',
      stay: 'Stay signed in',
      signIn: 'Sign in again',
    });

    expect(
      logoutWarningText({
        reason: 'idle',
        changes: 3,
        unapplied: '',
        drafts,
        canStay: true,
        secondsLeft: 60,
      }),
    ).toMatchObject({
      title: 'You will be logged out',
      message:
        'You have been inactive for a while. 3 changes to Builder drafts have not reached the server. Logging out deletes them from this browser. Use Download to keep a copy.',
      confirm: 'Log out now',
    });

    const unapplied =
      'Your changes to Device web01 in the Inspector cannot be saved until Memory is fixed.';

    // Download would not hold the Inspector's edits: staying is the way to
    // keep them, when there is one.
    expect(
      logoutWarningText({
        reason: 'expired',
        changes: 0,
        unapplied,
        drafts: [],
        canStay: false,
        secondsLeft: 60,
      }),
    ).toMatchObject({
      title: 'Your session has expired',
      message: `To sign in again, you must log out. ${unapplied} Logging out loses them.`,
      confirm: 'Log out now',
    });
    expect(
      logoutWarningText({
        reason: 'manual',
        changes: 0,
        unapplied,
        drafts: [],
        canStay: true,
        secondsLeft: null,
      }).message,
    ).toBe(`${unapplied} Logging out loses them. Stay signed in to fix them.`);

    // A session that expired while the user asked to log out.
    expect(
      logoutWarningText({
        reason: 'expired',
        changes: 1,
        unapplied: '',
        drafts,
        canStay: false,
        secondsLeft: null,
      }),
    ).toMatchObject({
      title: 'Your session has expired',
      message:
        'To sign in again, you must log out. 1 change to Builder drafts has not reached the server. Logging out deletes it from this browser. Use Download to keep a copy.',
      confirm: 'Log out anyway',
    });

    expect(
      logoutWarningText({
        reason: 'manual',
        changes: 1,
        unapplied,
        drafts,
        canStay: true,
        secondsLeft: null,
      }).message,
    ).toBe(
      `1 change to Builder drafts has not reached the server. ${unapplied} Logging out deletes them from this browser. Use Download to keep a copy.`,
    );

    // Changes this browser cannot rebuild a diagram from: no Download.
    expect(
      logoutWarningText({
        reason: 'manual',
        changes: 1,
        unapplied: '',
        drafts: [],
        canStay: true,
        secondsLeft: null,
      }).message,
    ).toBe(
      '1 change to Builder drafts has not reached the server. Logging out deletes it from this browser.',
    );

    // On the Builder's page, signing in again keeps them, and fixes the
    // Inspector's.
    expect(
      logoutWarningText({
        reason: 'expired',
        changes: 2,
        unapplied: '',
        drafts,
        canStay: false,
        canSignIn: true,
        secondsLeft: 60,
      }).message,
    ).toBe(
      '2 changes to Builder drafts have not reached the server. Sign in again to save them. Logging out deletes them from this browser. Use Download to keep a copy.',
    );
    expect(
      logoutWarningText({
        reason: 'expired',
        changes: 0,
        unapplied,
        drafts: [],
        canStay: false,
        canSignIn: true,
        secondsLeft: 60,
      }).message,
    ).toBe(`${unapplied} Logging out loses them. Sign in again to fix them.`);

    expect(countdownText(60)).toBe('Logging out in 60 seconds.');
    expect(countdownText(1)).toBe('Logging out in 1 second.');
  });

  test('the countdown goes before the page title while it runs', () => {
    const title = 'Lab - Builder - phēnix';
    const counting = countdownTitle(title, 42);

    expect(counting).toBe(`Logging out in 42 seconds – ${title}`);
    expect(countdownTitle(counting, 1)).toBe(
      `Logging out in 1 second – ${title}`,
    );
    expect(countdownTitle(counting, null)).toBe(title);
    expect(countdownTitle(title, null)).toBe(title);
  });

  async function render(warning) {
    phenix.state = reactive({
      username: 'alice',
      logoutWarning: warning,
      answerLogoutWarning: vi.fn(),
    });

    return renderToString(createSSRApp({ render: () => h(LogoutWarning) }));
  }

  function tag(html, pattern) {
    const found = html.match(new RegExp(`<[a-z0-9]+\\b[^>]*${pattern}[^>]*>`));

    expect(found, pattern).not.toBeNull();

    return found[0];
  }

  const idle = {
    reason: 'idle',
    changes: 2,
    unapplied: '',
    drafts: [{ key: 'k1', name: 'lab.json' }],
    canStay: true,
    secondsLeft: 60,
    notice: '',
  };

  const buttons = (html) =>
    [...html.matchAll(/<button[^>]*>\s*([^<]*?)\s*<\/button>/g)].map(
      (match) => match[1],
    );

  test('is an alert dialog whose description holds the message and the countdown', async () => {
    const html = await render(idle);
    const dialog = tag(html, 'role="alertdialog"');

    expect(dialog).toContain('aria-modal="true"');
    expect(dialog).toContain('aria-labelledby="logout-warning-title"');
    expect(dialog).toContain(
      'aria-describedby="logout-warning-message logout-warning-countdown"',
    );
    expect(dialog).toContain('data-theme="light"');
    expect(html).toMatch(
      /<h2 id="logout-warning-title"[^>]*>You will be logged out<\/h2>/,
    );
    expect(html).toMatch(
      /<p id="logout-warning-message"[^>]*>You have been inactive for a while\. 2 changes/,
    );
    expect(html).toMatch(
      /id="logout-warning-countdown"[^>]*>\s*Logging out in 60 seconds\.\s*</,
    );
    // The regions that speak later are there, empty, from the start.
    expect(tag(html, 'role="status"')).not.toBeNull();
    expect(tag(html, 'aria-live="assertive"')).toContain('aria-atomic="true"');
    expect(buttons(html)).toEqual([
      'Download',
      'Stay signed in',
      'Log out now',
    ]);
  });

  test('offers one Download per draft, named when there are several, and none without a draft to save', async () => {
    let html = await render({
      ...idle,
      drafts: [
        { key: 'k1', name: 'lab.json' },
        { key: 'k2', name: 'lab-2.json' },
      ],
    });

    expect(buttons(html)).toEqual([
      'Download lab.json',
      'Download lab-2.json',
      'Stay signed in',
      'Log out now',
    ]);

    html = await render({
      ...idle,
      changes: 0,
      unapplied: 'Your changes to Device web01 cannot be saved.',
      drafts: [],
    });
    expect(buttons(html)).toEqual(['Stay signed in', 'Log out now']);
    expect(html).toContain('Stay signed in to fix them.');
    expect(html).not.toContain('Download');
  });

  test('says the near-end notice, and offers no Stay once the session has expired', async () => {
    const html = await render({
      ...idle,
      reason: 'expired',
      canStay: false,
      secondsLeft: 10,
      notice: 'Logging out in 10 seconds.',
    });

    expect(html).toMatch(
      /aria-live="assertive"[^>]*>\s*Logging out in 10 seconds\.\s*</,
    );
    expect(html).toContain('To sign in again, you must log out.');
    expect(html).not.toContain('Stay signed in');
    expect(html).not.toContain('Sign in again');
  });

  test("offers Sign in again, before Log out now, when the Builder's page can sign in again", async () => {
    const html = await render({
      ...idle,
      reason: 'expired',
      canStay: false,
      canSignIn: true,
    });

    expect(buttons(html)).toEqual(['Download', 'Sign in again', 'Log out now']);
    expect(tag(html, 'data-testid="logout-warning-signin"')).toContain(
      'type="button"',
    );
    expect(html).toContain('Sign in again to save them.');
    expect(html).not.toContain('you must log out');
  });

  test('a logout the user asked for has no countdown', async () => {
    const html = await render({ ...idle, reason: 'manual', secondsLeft: null });

    expect(tag(html, 'role="alertdialog"')).toContain(
      'aria-describedby="logout-warning-message"',
    );
    expect(html).not.toContain('logout-warning-countdown');
    expect(html).toContain('Log out anyway');
  });

  test('is not there without a warning', async () => {
    expect(await render(null)).not.toContain('<dialog');
  });
});

// Signing in again without leaving the Builder (see builder/signin.js).
describe("the Builder's sign-in", () => {
  async function render(state) {
    phenix.state = reactive({
      username: 'alice',
      auth: true,
      token: `h.${btoa(JSON.stringify({ exp: 4102444800 }))}.s`,
      loggingOut: false,
    });
    Object.assign(signIn, state);

    const app = createSSRApp({ render: () => h(BuilderSignIn) });

    app.use(createPinia());

    try {
      return await renderToString(app);
    } finally {
      // A server render never unmounts: the page it hosted goes, as
      // another's would, and takes the dialog's state with it.
      hostSignIn({ available: () => false })();
    }
  }

  const tag = (html, pattern) =>
    html.match(new RegExp(`<[a-z0-9]+\\b[^>]*${pattern}[^>]*>`))?.[0] || '';

  test("asks for the same user's password in a labelled modal form, the username shown, not typed", async () => {
    const html = await render({ open: true, needed: true });
    const dialog = tag(html, 'data-testid="builder-signin"');

    expect(dialog).toMatch(/^<dialog/);
    expect(dialog).toContain('aria-modal="true"');
    expect(dialog).toContain('aria-labelledby="builder-signin-title"');
    expect(dialog).toContain('aria-describedby="builder-signin-message"');
    expect(html).toMatch(/<h2 id="builder-signin-title"[^>]*>Sign in again</);

    const user = tag(html, 'id="builder-signin-user"');

    expect(user).toContain('value="alice"');
    expect(user).toContain('readonly');
    expect(user).toContain('autocomplete="username"');
    expect(html).toMatch(/<label for="builder-signin-user"[^>]*>Username</);

    const password = tag(html, 'id="builder-signin-password"');

    expect(password).toContain('type="password"');
    expect(password).toContain('autocomplete="current-password"');
    expect(html).toMatch(/<label for="builder-signin-password"[^>]*>Password</);
    // Rendered, empty, from the start, so it is known before its first
    // message.
    expect(tag(html, 'id="builder-signin-error"')).toContain('role="alert"');
    expect(tag(html, 'data-testid="signin-submit"')).toContain('type="submit"');
    expect(html).not.toContain('builder-signin-notice');
  });

  test('once declined, a notice offers it again; nothing shows while the session goes on', async () => {
    const html = await render({ needed: true, declined: true });

    expect(html).toContain('data-testid="builder-signin-notice"');
    expect(tag(html, 'data-testid="signin-again"')).toContain(
      'aria-haspopup="dialog"',
    );
    expect(html).not.toContain('<dialog');
    expect(await render({})).not.toMatch(/<(dialog|div)/);
  });
});

describe("the Builder's changes this browser holds", () => {
  // A record as the save queue stores it, with `ops` queued.
  function record(actor, owner, draftId, ops = [], entries = []) {
    return {
      key: draftKey(actor, owner, draftId),
      actor,
      owner,
      draftId,
      etag: '"e1"',
      serverHead: null,
      cursor: 0,
      entries,
      queue: ops,
      updatedAt: '2026-09-27T00:00:00.000Z',
    };
  }

  const snapshot = (id) => ({
    opId: id,
    kind: 'snapshot',
    commitId: id,
    label: `Edit ${id}`,
  });
  const entry = (id, name) => ({
    id,
    label: `Edit ${id}`,
    snapshot: { name, nodes: [], edges: [] },
  });

  async function stored(...records) {
    const draftStore = createMemoryStore();

    for (const item of records) {
      await draftStore.put(item, { write: item.entries });
    }

    return draftStore;
  }

  // The draft open in the Builder, holding `pending` changes.
  function openDraft({
    pending = 0,
    blocked = null,
    editing = true,
    readOnly = false,
  } = {}) {
    const draft = {
      store: {
        readOnly,
        doc: { name: 'Open lab' },
        autosave: {
          record: {
            key: draftKey('alice', 'alice', 'd1'),
            queue: Array.from({ length: pending }, (_, i) => snapshot(`o${i}`)),
          },
        },
        saveNow: vi.fn(async () => {
          draft.store.autosave.record.queue = [];
        }),
      },
      editing: () => editing,
      saveUnapplied: vi.fn(() => blocked),
      describe: (unapplied) => `Cannot save ${unapplied.fields.join(', ')}.`,
    };

    draft.unregister = registerOpenDraft(draft);

    return draft;
  }

  let unregister = () => {};

  afterEach(() => {
    unregister();
  });

  test("counts the user's queued changes, whether or not the Builder is open", async () => {
    const draftStore = await stored(
      record('alice', 'alice', 'd1', [snapshot('a'), snapshot('b')]),
      record('alice', 'bob', 'd2', [snapshot('c')]),
      record('alice', 'alice', 'd3', []),
      record('bob', 'bob', 'd4', [snapshot('d')]),
    );

    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toEqual({ changes: 3, unapplied: '', drafts: [] });
  });

  // A page left before IndexedDB stored its last change kept a copy of it
  // in localStorage: logout clears it too, so it counts.
  test('counts the changes a page kept as it was left, and Download can save them', async () => {
    const items = new Map();
    const storage = {
      get length() {
        return items.size;
      },
      key: (index) => [...items.keys()][index] ?? null,
      getItem: (key) => items.get(key) ?? null,
      setItem: (key, value) => items.set(key, value),
      removeItem: (key) => items.delete(key),
    };
    const draftStore = createMemoryStore({ storage });
    const saved = record(
      'alice',
      'alice',
      'd2',
      [snapshot('a')],
      [entry('a', 'Kept lab')],
    );

    await draftStore.put(saved, { write: saved.entries });
    draftStore.keep(
      {
        ...saved,
        queue: [snapshot('a'), snapshot('b')],
        entries: [{ id: 'a', label: 'Edit a' }, entry('b', 'Kept lab 2')],
      },
      { write: [entry('b', 'Kept lab 2')] },
    );
    const kept = record(
      'alice',
      'bob',
      'd3',
      [snapshot('c')],
      [entry('c', 'Copied lab')],
    );

    draftStore.keep(kept, { write: kept.entries });

    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toEqual({
      changes: 3,
      unapplied: '',
      drafts: [
        { key: saved.key, name: 'kept-lab-2.json' },
        { key: kept.key, name: 'copied-lab.json' },
      ],
    });
  });

  test('the open draft saves what the Inspector holds, and its queue counts once', async () => {
    const draftStore = await stored(
      record('alice', 'alice', 'd1', [snapshot('a')]),
      record('alice', 'alice', 'd2', [snapshot('b')]),
    );
    const draft = openDraft({
      pending: 2,
      blocked: { title: 'Device web01', fields: ['Memory'] },
    });

    unregister = draft.unregister;
    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toEqual({
      changes: 3,
      unapplied: 'Cannot save Memory.',
      drafts: [
        { key: draftKey('alice', 'alice', 'd1'), name: 'open-lab.json' },
      ],
    });
    expect(draft.saveUnapplied).toHaveBeenCalledTimes(1);
    expect(draft.store.saveNow).not.toHaveBeenCalled();

    // Closed, the Builder is not asked, and its record counts.
    draft.unregister();
    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toEqual({ changes: 2, unapplied: '', drafts: [] });
    expect(draft.saveUnapplied).toHaveBeenCalledTimes(1);

    // Neither is a draft that is only listed, or read only.
    for (const options of [{ editing: false }, { readOnly: true }]) {
      const other = openDraft({ pending: 1, ...options });

      await unsentBuilderWork({ username: 'alice', draftStore });
      expect(other.saveUnapplied).not.toHaveBeenCalled();
      other.unregister();
    }
  });

  test('sending first waits a moment at most, then counts what is left', async () => {
    vi.useFakeTimers();

    const other = record('alice', 'alice', 'd2', [snapshot('b')]);
    const draftStore = await stored(other);
    const draft = openDraft({ pending: 1 });
    const sendQueued = vi.fn(() => new Promise(() => {}));

    unregister = draft.unregister;
    draft.store.saveNow.mockReturnValue(new Promise(() => {}));

    const found = unsentBuilderWork({
      username: 'alice',
      send: true,
      draftStore,
      sendQueued,
      openTabs: async () => new Set(),
    });

    await pass(LOGOUT_SEND_WAIT_MS - 1);
    expect(sendQueued).toHaveBeenCalledWith(
      [expect.objectContaining({ key: other.key })],
      'alice',
      draftStore,
    );
    expect(draft.store.saveNow).toHaveBeenCalledTimes(1);

    let settled = false;

    found.then(() => {
      settled = true;
    });
    await pass();
    expect(settled).toBe(false);

    await pass(1);
    await expect(found).resolves.toMatchObject({ changes: 2, unapplied: '' });

    // Without sending, nothing is sent.
    await unsentBuilderWork({ username: 'alice', draftStore, sendQueued });
    expect(sendQueued).toHaveBeenCalledTimes(1);
  });

  // Another open tab sends its own changes, once the user chooses which to
  // save (see tabs.js): logout counts them, but sends only the queues no
  // open tab holds.
  test("another open tab's changes count, but are left to that tab to send", async () => {
    const open = {
      ...record('alice', 'alice', 'd2', [snapshot('b'), snapshot('c')]),
      key: `${draftKey('alice', 'alice', 'd2')}#B`,
      tab: 'B',
    };
    const closed = {
      ...record('alice', 'alice', 'd2', [snapshot('z')]),
      key: `${draftKey('alice', 'alice', 'd2')}#Z`,
      tab: 'Z',
    };
    const draftStore = await stored(open, closed);
    const sendQueued = vi.fn(async () => {});

    await expect(
      unsentBuilderWork({
        username: 'alice',
        send: true,
        draftStore,
        sendQueued,
        openTabs: async () => new Set(['B']),
      }),
    ).resolves.toMatchObject({ changes: 3 });
    expect(sendQueued).toHaveBeenCalledWith(
      [expect.objectContaining({ key: closed.key })],
      'alice',
      draftStore,
    );
  });

  // Without Web Locks, the tabs open are those that answer over a
  // BroadcastChannel (see presentTabs in tabs.js).
  test("without Web Locks, another open tab's changes are still left to it", async () => {
    // Every other channel of the same name hears a message, a task later.
    const channels = new Set();

    class Channel {
      constructor(name) {
        this.name = name;
        this.listeners = new Set();
        channels.add(this);
      }

      postMessage(data) {
        for (const other of channels) {
          if (other !== this && other.name === this.name) {
            setTimeout(() => other.listeners.forEach((fn) => fn({ data })), 0);
          }
        }
      }

      addEventListener(_, fn) {
        this.listeners.add(fn);
      }

      removeEventListener(_, fn) {
        this.listeners.delete(fn);
      }

      close() {
        channels.delete(this);
      }
    }

    vi.useFakeTimers();
    vi.stubGlobal('navigator', {});
    vi.stubGlobal('window', {
      navigator: {},
      BroadcastChannel: Channel,
      addEventListener() {},
      removeEventListener() {},
    });

    try {
      // Tab B is open, and answers.
      answerPresence(openChannel('tabs', { Channel }), 'B');

      const open = {
        ...record('alice', 'alice', 'd2', [snapshot('b')]),
        key: `${draftKey('alice', 'alice', 'd2')}#B`,
        tab: 'B',
      };
      const closed = {
        ...record('alice', 'alice', 'd2', [snapshot('z')]),
        key: `${draftKey('alice', 'alice', 'd2')}#Z`,
        tab: 'Z',
      };
      const draftStore = await stored(open, closed);
      const sendQueued = vi.fn(async () => {});
      const work = unsentBuilderWork({
        username: 'alice',
        send: true,
        draftStore,
        sendQueued,
      });

      await vi.advanceTimersByTimeAsync(ANSWER_WAIT_MS);
      await expect(work).resolves.toMatchObject({ changes: 2 });
      expect(sendQueued).toHaveBeenCalledWith(
        [expect.objectContaining({ key: closed.key })],
        'alice',
        draftStore,
      );
    } finally {
      vi.unstubAllGlobals();
    }
  });

  test('a draft that is not open is sent by a save queue of its own', async () => {
    const other = record(
      'alice',
      'alice',
      'd2',
      [snapshot('b')],
      [entry('b', 'Other lab')],
    );
    const draftStore = await stored(other);

    api.appendSnapshot.mockResolvedValue({
      etag: '"e2"',
      draft: { snapshotId: 's2' },
    });

    await expect(
      unsentBuilderWork({ username: 'alice', send: true, draftStore }),
    ).resolves.toEqual({ changes: 0, unapplied: '', drafts: [] });
    expect(api.appendSnapshot).toHaveBeenCalledWith(
      'alice',
      'd2',
      {
        document: { name: 'Other lab', nodes: [], edges: [] },
        summary: 'Edit b',
        opId: 'b',
      },
      '"e1"',
    );
    await expect(draftStore.all()).resolves.toEqual([]);
  });

  // Back to drafts leaves the draft's queue sending in the background: it
  // sends its own changes, which count once, and Download has them.
  test('a draft closed while its changes are sent is sent by its own queue, and counts once', async () => {
    const closed = record(
      'alice',
      'alice',
      'd2',
      [snapshot('b'), snapshot('c')],
      [entry('b', 'Closed lab'), entry('c', 'Closed lab 2')],
    );
    const draftStore = await stored(
      closed,
      record('alice', 'alice', 'd3', [snapshot('d')]),
    );
    const queue = {
      record: structuredClone(closed),
      flush: vi.fn(async () => {
        queue.record.queue = queue.record.queue.slice(1);
      }),
    };
    const sendQueued = vi.fn(async () => {});

    unregister = registerQueue(queue);

    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toEqual({
      changes: 3,
      unapplied: '',
      drafts: [{ key: closed.key, name: 'closed-lab-2.json' }],
    });
    await expect(
      draftExport({
        username: 'alice',
        key: closed.key,
        name: 'closed-lab-2.json',
        draftStore,
      }),
    ).resolves.toMatchObject({ text: expect.stringContaining('Closed lab 2') });

    await expect(
      unsentBuilderWork({
        username: 'alice',
        send: true,
        draftStore,
        sendQueued,
      }),
    ).resolves.toMatchObject({ changes: 2 });
    expect(queue.flush).toHaveBeenCalledTimes(1);
    expect(sendQueued).toHaveBeenCalledWith(
      [expect.objectContaining({ draftId: 'd3' })],
      'alice',
      draftStore,
    );
  });

  test('Download has the diagram each queue leaves, and the open one as it is shown', async () => {
    const d2 = record(
      'alice',
      'alice',
      'd2',
      [
        snapshot('b'),
        snapshot('c'),
        { opId: 'u', kind: 'cursor', commitId: 'b' },
      ],
      [entry('b', 'Open lab'), entry('c', 'After')],
    );
    const draftStore = await stored(
      d2,
      // A move to a snapshot only the server holds leaves nothing here.
      record('alice', 'alice', 'd3', [
        { opId: 'v', kind: 'cursor', snapshotId: 's9' },
      ]),
      record('bob', 'bob', 'd4', [snapshot('d')], [entry('d', 'Bob lab')]),
    );
    const bob = draftKey('bob', 'bob', 'd4');

    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toMatchObject({
      drafts: [{ key: d2.key, name: 'open-lab.json' }],
    });

    // Only a draft with changes to send is offered; two that share a name
    // are told apart.
    const open = openDraft();

    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toMatchObject({ drafts: [{ key: d2.key }] });
    open.unregister();

    const draft = openDraft({ pending: 1 });
    const openKey = draftKey('alice', 'alice', 'd1');

    unregister = draft.unregister;
    await expect(
      unsentBuilderWork({ username: 'alice', draftStore }),
    ).resolves.toMatchObject({
      drafts: [
        { key: openKey, name: 'open-lab.json' },
        { key: d2.key, name: 'open-lab-2.json' },
      ],
    });

    await expect(
      draftExport({
        username: 'alice',
        key: d2.key,
        name: 'open-lab-2.json',
        draftStore,
      }),
    ).resolves.toEqual({
      name: 'open-lab-2.json',
      text: `${JSON.stringify({ name: 'Open lab', nodes: [], edges: [] }, null, 2)}\n`,
    });
    await expect(
      draftExport({
        username: 'alice',
        key: openKey,
        name: 'open-lab.json',
        draftStore,
      }),
    ).resolves.toEqual({
      name: 'open-lab.json',
      text: '{\n  "name": "Open lab"\n}\n',
    });
    // Nor another user's, or one no longer here.
    await expect(
      draftExport({ username: 'alice', key: bob, name: 'x.json', draftStore }),
    ).resolves.toBeNull();
    await expect(
      draftExport({
        username: 'alice',
        key: 'gone',
        name: 'x.json',
        draftStore,
      }),
    ).resolves.toBeNull();
  });

  test('is saved as Download saves JSON', () => {
    expect(diagramFile({ name: '  Core Lab: v2! ' })).toEqual({
      name: 'core-lab-v2.json',
      text: '{\n  "name": "  Core Lab: v2! "\n}\n',
    });
    expect(diagramFile({}).name).toBe('topology.json');
  });
});
