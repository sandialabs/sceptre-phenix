// Shared operation feedback: whether an action can be taken and why not, what
// a selection skips, one confirm dialog, and one summary of how it went.
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/notify.js', () => ({ openNotification: vi.fn() }));

import { openNotification } from '@/utils/notify.js';
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
import { fakeBuefy, lastConfirm, toasts } from './helpers/buefy.js';
import { httpError } from './helpers/fixtures.js';

beforeEach(() => openNotification.mockReset());

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
  it('asks once, naming what it skips, and resolves with the answer', async () => {
    const buefy = fakeBuefy();
    const answer = confirmOperation(buefy, {
      title: 'Restart VMs',
      message: 'Restart <b>2 VMs</b>?',
      confirmText: 'Restart',
      skipped: [{ reason: 'the VM is paused', names: ['<db>'] }],
      noun: 'VM',
    });

    const dialog = lastConfirm({ $buefy: buefy });
    expect(buefy.dialog.confirm).toHaveBeenCalledTimes(1);
    expect(dialog).toMatchObject({
      title: 'Restart VMs',
      confirmText: 'Restart',
    });
    expect(dialog.message).toContain('Restart <b>2 VMs</b>?');
    expect(dialog.message).toContain('The &lt;db&gt; VM is paused.');

    dialog.onConfirm();
    await expect(answer).resolves.toBe(true);
  });

  it('resolves false when canceled, and says nothing of skips when there are none', async () => {
    const buefy = fakeBuefy();
    const answer = confirmOperation(buefy, {
      title: 'Clear',
      message: 'Clear?',
      confirmText: 'Clear',
      noun: 'run',
    });
    const dialog = lastConfirm({ $buefy: buefy });
    expect(dialog.message).toBe('<p>Clear?</p>');
    dialog.onCancel();
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
  const ok = (name) => ({ name, status: 'succeeded' });

  // the text of a VNode tree
  const text = (node) =>
    node == null || node === false
      ? ''
      : typeof node === 'string'
        ? node
        : Array.isArray(node)
          ? node.map(text).join('|')
          : text(node.children);

  const findButton = (node) => {
    if (!node || typeof node !== 'object') return null;
    if (Array.isArray(node)) {
      return node.map(findButton).find(Boolean) ?? null;
    }
    if (node.type === 'button') return node;
    return findButton(node.children);
  };

  it('toasts when every item succeeded', () => {
    const buefy = fakeBuefy();
    notifyOutcome(buefy, {
      verbPast: 'Restarted',
      noun: 'VM',
      items: [ok('a'), ok('b'), ok('c')],
    });
    expect(toasts({ $buefy: buefy })).toEqual([
      expect.objectContaining({
        message: 'Restarted 3 VMs',
        type: 'is-success',
      }),
    ]);
    expect(openNotification).not.toHaveBeenCalled();
  });

  it('shows nothing for no items', () => {
    const buefy = fakeBuefy();
    notifyOutcome(buefy, { verbPast: 'Restarted', noun: 'VM', items: [] });
    expect(buefy.toast.open).not.toHaveBeenCalled();
    expect(openNotification).not.toHaveBeenCalled();
  });

  it('shows one lasting notification naming the failures, and retries those worth it', () => {
    const buefy = fakeBuefy();
    const onRetry = vi.fn();
    const close = vi.fn();
    openNotification.mockReturnValue({ close });

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

    expect(buefy.toast.open).not.toHaveBeenCalled();
    expect(openNotification).toHaveBeenCalledTimes(1);
    const options = openNotification.mock.lastCall[0];
    expect(options).toMatchObject({ indefinite: true, type: 'is-warning' });
    expect(text(options.message)).toBe(
      'Restarted 1 of 3 VMs; 2 failed|b: locked|c: boom|Retry failed (1)',
    );

    findButton(options.message).props.onClick();
    expect(close).toHaveBeenCalled();
    expect(onRetry).toHaveBeenCalledWith(['b']);
  });

  it(`names at most ${LISTED_FAILURES} failures, and offers no retry without one worth it`, () => {
    const items = Array.from({ length: LISTED_FAILURES + 3 }, (_, i) => ({
      name: `vm${i}`,
      status: 'failed',
      error: 'boom',
    }));
    notifyOutcome(fakeBuefy(), {
      verbPast: 'Restarted',
      noun: 'VM',
      items,
      onRetry: vi.fn(),
    });
    const options = openNotification.mock.lastCall[0];
    expect(options.type).toBe('is-danger');
    expect(text(options.message)).toMatch(/\|vm7: boom\|and 3 more$/);
    expect(findButton(options.message)).toBe(null);
  });
});
