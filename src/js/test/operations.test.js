// Shared operation feedback: whether an action can be taken and why not, what
// a selection skips, one confirm dialog, and one summary of how it went.
import { nextTick } from 'vue';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  LISTED_FAILURES,
  actionState,
  confirmOperation,
  errorText,
  isRetryable,
  notifyOutcome,
  outcomeFromJob,
  outcomeFromSettled,
  outcomeTitle,
  partitionForAction,
  skippedText,
  summarizeOutcome,
} from '@/utils/operations.js';
import { httpError } from './helpers/fixtures.js';
import {
  confirmDialog,
  dialogs,
  mountComponent,
  textOf,
  toasts,
} from './helpers/mount.js';

// the $buefy of a mounted component, which also opens the notifications
const realBuefy = async () =>
  (await mountComponent({ template: '<div />' })).vm.$buefy;

const restart = { name: 'restart', label: 'Restart', icon: 'sync-alt' };

describe('actionState', () => {
  it.each([
    ['offers an action it may take', {}, true, false, null],
    [
      'says why the role may not',
      { permitted: false },
      false,
      true,
      "Can't restart: you don't have permission",
    ],
    [
      'says why the state rules it out, still offering it',
      { blocked: 'the VM is paused' },
      true,
      true,
      "Can't restart: the VM is paused",
    ],
    [
      'puts what is unavailable before permission',
      { unavailable: 'external nodes are not VMs', permitted: false },
      false,
      true,
      "Can't restart: external nodes are not VMs",
    ],
    [
      'puts permission before the state',
      { permitted: false, blocked: 'the VM is paused' },
      false,
      true,
      "Can't restart: you don't have permission",
    ],
  ])('%s', (_, opts, permitted, disabled, reason) => {
    expect(actionState(restart, opts)).toEqual({
      name: 'restart',
      label: 'Restart',
      description: undefined,
      icon: 'sync-alt',
      type: null,
      outlined: false,
      permitted,
      disabled,
      reason,
    });
  });

  it('uses the verb an action gives', () => {
    const state = actionState(
      {
        name: 'commit',
        label: 'Backing image',
        verb: 'create a backing image',
      },
      { blocked: 'the VM is not running' },
    );
    expect(state.reason).toBe(
      "Can't create a backing image: the VM is not running",
    );
  });
});

describe('partitionForAction and skippedText', () => {
  const runs = new Map([
    ['r1', { done: true }],
    ['r2', { done: false }],
    ['r3', { done: false }],
  ]);
  const split = (names) =>
    partitionForAction(
      names,
      (name) => runs.get(name),
      (run) => (run.done ? null : 'the run is still running'),
      'the run is not listed',
    );

  it('keeps those it can act on and groups the rest by reason', () => {
    expect(split(['r1', 'r2', 'r3', 'r9'])).toEqual({
      allowed: ['r1'],
      skipped: [
        { reason: 'the run is still running', names: ['r2', 'r3'] },
        { reason: 'the run is not listed', names: ['r9'] },
      ],
    });
  });

  it.each([
    [['r2'], 'The r2 run is still running.'],
    [['r2', 'r3'], 'The runs r2 and r3 are still running.'],
    [['r2', 'r3', 'r9'], 'The runs r2, r3, and r9 are still running.'],
  ])('says a reason of %j', (names, want) => {
    expect(
      skippedText([{ reason: 'the run is still running', names }], 'run'),
    ).toBe(want);
  });

  it('joins several reasons, and leaves a reason of another subject as is', () => {
    expect(
      skippedText(
        [
          { reason: 'the run does not exist', names: ['a', 'b'] },
          { reason: 'it needs a stopped experiment', names: ['c'] },
        ],
        'run',
      ),
    ).toBe('The runs a and b do not exist. it needs a stopped experiment.');
  });
});

describe('confirmOperation', () => {
  // the HTML of a dialog's message
  const messageOf = (dialog) =>
    dialog.querySelector('.media-content p > div').innerHTML;

  it('asks once, naming what it skips, and resolves with the answer', async () => {
    const answer = confirmOperation(await realBuefy(), {
      title: 'Restart VMs',
      message: 'Restart <b>2 VMs</b>?',
      confirmText: 'Restart',
      skipped: [{ reason: 'the VM is paused', names: ['<db>'] }],
      noun: 'VM',
    });
    await nextTick();

    expect(dialogs()).toHaveLength(1);
    const [dialog] = dialogs();
    expect(textOf(dialog.querySelector('.modal-card-title'))).toBe(
      'Restart VMs',
    );
    expect(textOf(dialog.querySelector('footer .button:last-child'))).toBe(
      'Restart',
    );
    const message = messageOf(dialog);
    expect(message).toContain('Restart <b>2 VMs</b>?');
    expect(message).toContain('The &lt;db&gt; VM is paused.');

    await confirmDialog();
    await expect(answer).resolves.toBe(true);
  });

  it('resolves false when canceled, and says nothing of skips when there are none', async () => {
    const answer = confirmOperation(await realBuefy(), {
      title: 'Clear',
      message: 'Clear?',
      confirmText: 'Clear',
      noun: 'run',
    });
    await nextTick();
    const [dialog] = dialogs();
    expect(messageOf(dialog)).toBe('<p>Clear?</p>');
    dialog.querySelector('footer .button:first-child').click();
    await expect(answer).resolves.toBe(false);
  });
});

describe('errorText', () => {
  it.each([
    ['a string', 'experiment not found', 'experiment not found', []],
    ['an Error', new Error('Network Error'), 'Network Error', []],
    [
      "a JSON error's message and cause",
      httpError(500, {
        message: 'starting experiment: 1 error occurred:\n\t* VM web',
        cause: 'disk missing',
      }),
      'starting experiment',
      ['VM web', 'disk missing'],
    ],
    [
      'a text body',
      httpError(409, 'vm a is locked with status starting'),
      'vm a is locked with status starting',
      [],
    ],
    ['a Blob body, by its status', httpError(500, new Blob(['x'])), '500', []],
    ['nothing', undefined, 'Unknown error', []],
  ])('reads %s', (_, err, title, steps) => {
    expect(errorText(err)).toEqual({ title, steps });
  });
});

describe('isRetryable', () => {
  it.each([
    ['a network error', { message: 'Network Error', request: {} }, true],
    ['a timeout', { code: 'ECONNABORTED', message: 'timeout' }, true],
    ['a busy resource', httpError(409, 'locked'), true],
    ['a locked resource', httpError(423, ''), true],
    ['too many requests', httpError(429, ''), true],
    ['a bad gateway', httpError(502, ''), true],
    ['an unavailable server', httpError(503, ''), true],
    ['a gateway timeout', httpError(504, ''), true],
    ['a bad request', httpError(400, 'bad'), false],
    ['a refusal', httpError(403, 'forbidden'), false],
    ['a missing thing', httpError(404, ''), false],
    ['a server error', httpError(500, 'boom'), false],
    ['a canceled request', { code: 'ERR_CANCELED', request: {} }, false],
    ['a plain error', new Error('bug'), false],
    ['a string', 'oops', false],
  ])('%s: %s', (_, err, want) => {
    expect(isRetryable(err)).toBe(want);
  });
});

describe('outcomes', () => {
  const items = [
    { name: 'a', status: 'succeeded', error: null, retryable: false },
    { name: 'b', status: 'failed', error: 'locked', retryable: true },
    { name: 'c', status: 'failed', error: 'boom', retryable: false },
    { name: 'd', status: 'skipped', error: "you don't have permission" },
    { name: 'e', status: 'canceled' },
  ];

  it('turns settled requests into items', () => {
    const results = [
      { status: 'fulfilled', value: {} },
      { status: 'rejected', reason: httpError(409, 'vm b is locked') },
      { status: 'rejected', reason: httpError(500, 'boom') },
    ];
    expect(outcomeFromSettled(['a', 'b', 'c'], results)).toEqual([
      { name: 'a', status: 'succeeded', error: null, retryable: false },
      { name: 'b', status: 'failed', error: 'vm b is locked', retryable: true },
      { name: 'c', status: 'failed', error: 'boom', retryable: false },
    ]);
  });

  it("passes a job's items through", () => {
    expect(
      outcomeFromJob({
        items: [{ name: 'b', status: 'failed', error: 'x', retryable: true }],
      }),
    ).toEqual([{ name: 'b', status: 'failed', error: 'x', retryable: true }]);
    expect(outcomeFromJob(null)).toEqual([]);
  });

  it('counts them', () => {
    expect(summarizeOutcome(items)).toEqual({
      succeeded: 1,
      failed: 2,
      skipped: 1,
      canceled: 1,
      retryable: ['b'],
    });
  });

  it.each([
    [[items[0]], 'Restarted 1 VM'],
    [[items[0], { ...items[0], name: 'z' }], 'Restarted 2 VMs'],
    [items, 'Restarted 1 of 5 VMs; 2 failed, 1 skipped, 1 canceled'],
  ])('sums them up', (list, want) => {
    expect(
      outcomeTitle({ verbPast: 'Restarted', noun: 'VM', items: list }),
    ).toBe(want);
  });
});

describe('notifyOutcome', () => {
  afterEach(() => vi.useRealTimers());

  const ok = (name) => ({ name, status: 'succeeded' });

  // the lasting notifications on the page body
  const notices = () => [...document.querySelectorAll('.notification')];

  // a notification's message: its title, failures and retry button
  const messageOf = (notice) =>
    notice.querySelector('.error-title').parentElement;
  const lines = (notice) => [...messageOf(notice).children].map(textOf);

  it('toasts when every item succeeded', async () => {
    notifyOutcome(await realBuefy(), {
      verbPast: 'Restarted',
      noun: 'VM',
      items: [ok('a'), ok('b'), ok('c')],
    });
    expect(toasts()).toEqual([{ text: 'Restarted 3 VMs', type: 'is-success' }]);
    expect(notices()).toEqual([]);
  });

  it('shows nothing for no items', async () => {
    notifyOutcome(await realBuefy(), {
      verbPast: 'Restarted',
      noun: 'VM',
      items: [],
    });
    expect(toasts()).toEqual([]);
    expect(notices()).toEqual([]);
  });

  it('shows one lasting notification naming the failures, and retries those worth it', async () => {
    const buefy = await realBuefy();
    const onRetry = vi.fn();
    vi.useFakeTimers();

    notifyOutcome(buefy, {
      verbPast: 'Restarted',
      noun: 'VM',
      items: [
        ok('a'),
        { name: 'b', status: 'failed', error: 'locked', retryable: true },
        { name: 'c', status: 'failed', error: 'boom', retryable: false },
      ],
      onRetry,
    });
    // a notification that is not lasting has closed by now
    vi.advanceTimersByTime(60_000);
    vi.useRealTimers();

    expect(toasts()).toEqual([]);
    expect(notices()).toHaveLength(1);
    const [notice] = notices();
    expect(notice.classList).toContain('is-warning');
    expect(lines(notice)).toEqual([
      'Restarted 1 of 3 VMs; 2 failed',
      'b: locked',
      'c: boom',
      'Retry failed (1)',
    ]);

    messageOf(notice).querySelector('button').click();
    expect(onRetry).toHaveBeenCalledWith(['b']);
    await vi.waitFor(() => expect(notices()).toEqual([]));
  });

  it(`names at most ${LISTED_FAILURES} failures, and offers no retry without one worth it`, async () => {
    const items = Array.from({ length: LISTED_FAILURES + 3 }, (_, i) => ({
      name: `vm${i}`,
      status: 'failed',
      error: 'boom',
    }));
    notifyOutcome(await realBuefy(), {
      verbPast: 'Restarted',
      noun: 'VM',
      items,
      onRetry: vi.fn(),
    });
    const [notice] = notices();
    expect(notice.classList).toContain('is-danger');
    expect(lines(notice).slice(-2)).toEqual(['vm7: boom', 'and 3 more']);
    expect(messageOf(notice).querySelector('button')).toBe(null);
  });
});
