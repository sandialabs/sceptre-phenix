// Shared operation feedback: whether an action can be taken and why not, what
// a selection skips, one confirm dialog, and one summary of how it went.
import { AxiosError } from 'axios';
import fc from 'fast-check';
import { nextTick } from 'vue';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  LISTED_FAILURES,
  actionState,
  confirmOperation,
  errorText,
  errorLine,
  isRetryable,
  notifyOutcome,
  outcomeFromJob,
  outcomeFromSettled,
  outcomeLines,
  outcomeTitle,
  partitionForAction,
  skippedText,
  summarizeOutcome,
} from '@/utils/operations.js';
import { setWsDown } from '@/utils/freshness.js';
import { SHUTTING_DOWN, UNREACHABLE } from '@/utils/unreachable.js';
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

const nameArb = fc.constantFrom('a', 'b', 'c', 'd', 'e', 'f');

// an operation's items, each name once, in these statuses
const outcomeItems = (
  statuses = ['succeeded', 'failed', 'skipped', 'canceled'],
) =>
  fc.uniqueArray(
    fc.record({
      name: fc.stringMatching(/^[a-z][a-z0-9]{0,4}$/),
      status: fc.constantFrom(...statuses),
      error: fc.option(fc.stringMatching(/^[a-z]{0,6}$/)),
      retryable: fc.boolean(),
    }),
    { selector: (item) => item.name, maxLength: 20, size: 'medium' },
  );
const withStatus = (items, status) => items.filter((i) => i.status === status);

describe('actionState', () => {
  it.each([
    ['offers an action it may take', {}, true, false, null],
    [
      'says why the role may not',
      { permitted: false },
      false,
      true,
      'Cannot restart: you do not have permission',
    ],
    [
      'says why the state rules it out, still offering it',
      { blocked: 'the VM is paused' },
      true,
      true,
      'Cannot restart: the VM is paused',
    ],
    [
      'puts what is unavailable before permission',
      { unavailable: 'external nodes are not VMs', permitted: false },
      false,
      true,
      'Cannot restart: external nodes are not VMs',
    ],
    [
      'puts permission before the state',
      { permitted: false, blocked: 'the VM is paused' },
      false,
      true,
      'Cannot restart: you do not have permission',
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
      'Cannot create a backing image: the VM is not running',
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

  it('splits every name once, keeping their order, by the reason each is skipped for', () => {
    // each known name's reason to skip it, or null to act on it
    const known = fc.dictionary(nameArb, fc.option(fc.constantFrom('x', 'y')));
    fc.assert(
      fc.property(fc.array(nameArb), known, (names, reasons) => {
        const reasonOf = (name) =>
          name in reasons ? reasons[name] : 'the run is not listed';
        const { allowed, skipped } = partitionForAction(
          names,
          (name) => (name in reasons ? { name } : undefined),
          (item) => reasons[item.name],
          'the run is not listed',
        );
        expect(allowed).toEqual(names.filter((name) => !reasonOf(name)));
        expect(skipped.map((group) => group.reason)).toEqual([
          ...new Set(names.map(reasonOf).filter(Boolean)),
        ]);
        for (const { reason, names: list } of skipped) {
          expect(list).toEqual(
            names.filter((name) => reasonOf(name) === reason),
          );
        }
      }),
    );
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

// Bulma's grey text classes and the colors they draw
const BULMA_TEXT = {
  'has-text-grey-darker': '#363636',
  'has-text-grey-dark': '#4a4a4a',
  'has-text-grey': '#7a7a7a',
  'has-text-grey-light': '#b5b5b5',
  'has-text-grey-lighter': '#dbdbdb',
};

// the WCAG contrast ratio of two #rrggbb colors
function contrast(a, b) {
  const luminance = (hex) => {
    const [r, g, bl] = [1, 3, 5].map((i) => {
      const c = parseInt(hex.slice(i, i + 2), 16) / 255;
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
  };
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

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

  it('writes what it skips in a color that reads on the dialog', async () => {
    confirmOperation(await realBuefy(), {
      title: 'Restart VMs',
      message: 'Restart?',
      confirmText: 'Restart',
      skipped: [{ reason: 'the VM is paused', names: ['db'] }],
      noun: 'VM',
    });
    await nextTick();
    const note = dialogs()[0].querySelector('.media-content p > div p + p');
    const colors = [...note.classList].filter((c) => c.startsWith('has-text-'));
    expect(colors).toHaveLength(1);
    // the dialog body is $background-ter (#5a5a5a); AA text needs 4.5:1
    expect(contrast(BULMA_TEXT[colors[0]], '#5a5a5a')).toBeGreaterThanOrEqual(
      4.5,
    );
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

describe('errorText and errorLine', () => {
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
      'a JSON error a request for text left unparsed',
      httpError(403, '{"message":"forbidden","cause":"role viewer"}'),
      'forbidden',
      ['role viewer'],
    ],
    [
      'a text body',
      httpError(409, 'vm a is locked with status starting'),
      'vm a is locked with status starting',
      [],
    ],
    ['a Blob body, by its status', httpError(500, new Blob(['x'])), '500', []],
    ['nothing', undefined, 'Unknown error', []],
    [
      'a request that got no response',
      new AxiosError('Network Error', 'ERR_NETWORK', {}, {}),
      UNREACHABLE,
      [],
    ],
  ])('reads %s', (_, err, title, steps) => {
    expect(errorText(err)).toEqual({ title, steps });
  });

  it('says the server is shutting down for a request it did not answer as it does', () => {
    setWsDown(true, 'shutdown');
    try {
      expect(
        errorLine(new AxiosError('Network Error', 'ERR_NETWORK', {}, {})),
      ).toBe(SHUTTING_DOWN);
    } finally {
      setWsDown(false);
    }
  });

  // a step of an error chain as the server words it
  const stepText = fc.stringMatching(/^[a-z0-9]+(?:[ ./-][a-z0-9]+){0,3}$/);
  // an error and the errors it wraps, as hashicorp/go-multierror nests them
  const { chain } = fc.letrec((tie) => ({
    chain: fc.record({
      text: stepText,
      causes: fc.oneof(
        { maxDepth: 3 },
        fc.constant([]),
        fc.array(tie('chain'), { minLength: 1, maxLength: 3 }),
      ),
    }),
  }));
  // what Go's fmt.Errorf("%s: %w") over go-multierror's ListFormatFunc writes
  const goError = ({ text, causes }) => {
    if (!causes.length) return text;
    const n = causes.length;
    const list = causes.map((cause) => `* ${goError(cause)}`).join('\n\t');
    return `${text}: ${n} error${n === 1 ? '' : 's'} occurred:\n\t${list}\n\n`;
  };
  const steps = ({ text, causes }) => [text, ...causes.flatMap(steps)];

  it('reads every step of a nested chain, outermost first', () => {
    fc.assert(
      fc.property(chain, (err) => {
        const [title, ...rest] = steps(err);
        expect(errorText(goError(err))).toEqual({ title, steps: rest });
        expect(errorLine(httpError(500, { message: goError(err) }))).toBe(
          [title, ...rest].join(': '),
        );
      }),
    );
  });

  it('reads anything thrown or answered without throwing, as isRetryable does', () => {
    // no own toString, valueOf or __proto__, which no error or JSON body has
    const any = fc.anything({
      key: fc.string().filter((k) => !/^(__proto__|toString|valueOf)$/.test(k)),
    });
    const answer = fc.record(
      { status: any, statusText: any, data: any },
      { requiredKeys: [], noNullPrototype: true },
    );
    const thrown = fc.oneof(
      any,
      fc.record(
        {
          message: any,
          code: any,
          request: any,
          isAxiosError: any,
          response: fc.oneof(answer, any),
        },
        { requiredKeys: [], noNullPrototype: true },
      ),
    );
    fc.assert(
      fc.property(thrown, (err) => {
        const { title, steps: rest } = errorText(err);
        expect(title).toMatch(/\S/);
        for (const step of rest) expect(step).toMatch(/\S/);
        expect(typeof isRetryable(err)).toBe('boolean');
      }),
    );
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
    { name: 'd', status: 'skipped', error: 'you do not have permission' },
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

  const BROKEN = {
    message: 'unable to start experiment foo',
    code: 'broken_disks',
    details: {
      disks: [
        {
          vm: 'host-01',
          drive: 'disk',
          image: 'kali.qc2',
          problem: 'backing file /phenix/images/ro_kali.qc2 is missing',
        },
        {
          vm: 'host-02',
          drive: 'cdrom',
          image: 'tools.iso',
          problem: 'CD-ROM image /phenix/images/tools.iso is missing',
        },
      ],
    },
  };

  it('counts the broken disks of a refused start, and lists each as a detail', () => {
    const results = [{ status: 'rejected', reason: httpError(400, BROKEN) }];
    expect(outcomeFromSettled(['foo'], results)).toEqual([
      {
        name: 'foo',
        status: 'failed',
        error: 'qemu cannot open 2 disks on 2 VMs',
        retryable: false,
        details: [
          'VM host-01: disk kali.qc2: backing file /phenix/images/ro_kali.qc2 is missing',
          'VM host-02: CD-ROM tools.iso: CD-ROM image /phenix/images/tools.iso is missing',
        ],
      },
    ]);
  });

  it("lists the broken disks of a job's VM as details", () => {
    const error =
      'broken VM disk images: VM web: disk a.qc2: backing file /b.qc2 is missing. ' +
      'VM web: CD-ROM t.iso: CD-ROM image /t.iso is missing';
    expect(
      outcomeFromJob({ items: [{ name: 'web', status: 'failed', error }] }),
    ).toEqual([
      {
        name: 'web',
        status: 'failed',
        error: 'qemu cannot open 2 disks',
        retryable: false,
        details: [
          'disk a.qc2: backing file /b.qc2 is missing',
          'CD-ROM t.iso: CD-ROM image /t.iso is missing',
        ],
      },
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

  it('counts each finished item once, and only failures as worth retrying', () => {
    const statuses = ['succeeded', 'failed', 'skipped', 'canceled'];
    fc.assert(
      fc.property(outcomeItems([...statuses, 'pending', 'running']), (list) => {
        const counts = summarizeOutcome(list);
        for (const status of statuses) {
          expect(counts[status]).toBe(withStatus(list, status).length);
        }
        expect(counts.retryable).toEqual(
          withStatus(list, 'failed')
            .filter((item) => item.retryable)
            .map((item) => item.name),
        );
      }),
    );
  });

  it('sums them up in a title whose counts add up to the whole', () => {
    const titled = /^Restarted (?:(\d+) of )?(\d+) VMs?(?:: (.+))?$/;
    fc.assert(
      fc.property(outcomeItems(), (list) => {
        const title = outcomeTitle({
          verbPast: 'Restarted',
          noun: 'VM',
          items: list,
        });
        const [, ok, total, rest] = title.match(titled);
        const named = (rest ?? '')
          .split(', ')
          .filter(Boolean)
          .map((part) => part.split(' '));
        expect(Number(total)).toBe(list.length);
        expect(Number(ok ?? total)).toBe(withStatus(list, 'succeeded').length);
        expect(ok === undefined).toBe(!named.length);
        for (const [count, status] of named) {
          expect(Number(count)).toBe(withStatus(list, status).length);
        }
        expect(named.map(([, status]) => status)).toEqual(
          ['failed', 'skipped', 'canceled'].filter(
            (s) => withStatus(list, s).length,
          ),
        );
      }),
    );
  });

  it(`names the first ${LISTED_FAILURES} failures and skips, in order, and counts the rest`, () => {
    fc.assert(
      fc.property(outcomeItems(), (list) => {
        const unsuccessful = list.filter((item) =>
          ['failed', 'skipped'].includes(item.status),
        );
        const lines = outcomeLines(list);
        const listed = unsuccessful.slice(0, LISTED_FAILURES);
        expect(lines.slice(0, listed.length)).toEqual(
          listed.map((item) => `${item.name}: ${item.error || item.status}`),
        );
        const more = unsuccessful.length - LISTED_FAILURES;
        expect(lines.slice(listed.length)).toEqual(
          more > 0 ? [`and ${more} more`] : [],
        );
      }),
    );
  });

  it.each([
    [[items[0]], 'Restarted 1 VM'],
    [[items[0], { ...items[0], name: 'z' }], 'Restarted 2 VMs'],
    [items, 'Restarted 1 of 5 VMs: 2 failed, 1 skipped, 1 canceled'],
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
      'Restarted 1 of 3 VMs: 2 failed',
      'b: locked',
      'c: boom',
      'Retry failed (1)',
    ]);

    messageOf(notice).querySelector('button').click();
    expect(onRetry).toHaveBeenCalledWith(['b']);
    await vi.waitFor(() => expect(notices()).toEqual([]));
  });

  it("shows an item's details in a disclosure, open when it is the only failure", async () => {
    const buefy = await realBuefy();
    const broken = (name) => ({
      name,
      status: 'failed',
      error: 'qemu cannot open 1 disk',
      details: ['disk a.qc2: backing file /b.qc2 is missing'],
    });

    notifyOutcome(buefy, {
      verbPast: 'Started',
      noun: 'VM',
      items: [broken('web')],
    });
    let [notice] = notices();
    let details = messageOf(notice).querySelectorAll('details');
    expect(lines(notice)).toEqual([
      'Started 0 of 1 VM: 1 failed',
      'web: qemu cannot open 1 disk',
      'Show 1 problemdisk a.qc2: backing file /b.qc2 is missing',
    ]);
    expect([...details].map((d) => d.open)).toEqual([true]);
    expect(details[0].querySelector('summary')).not.toBe(null);
    notice.querySelector('.delete').click();
    await vi.waitFor(() => expect(notices()).toEqual([]));

    notifyOutcome(buefy, {
      verbPast: 'Started',
      noun: 'VM',
      items: [broken('web'), broken('db')],
    });
    [notice] = notices();
    details = messageOf(notice).querySelectorAll('details');
    expect([...details].map((d) => d.open)).toEqual([false, false]);
  });

  it('keeps its lasting notification when later toasts come and go', async () => {
    const buefy = await realBuefy();
    vi.useFakeTimers();
    notifyOutcome(buefy, {
      verbPast: 'Stopped',
      noun: 'experiment',
      items: [ok('a'), { name: 'b', status: 'failed', error: 'boom' }],
    });
    buefy.toast.open({ message: 'Experiment c is being updated' });
    buefy.toast.open({ message: 'Experiment d is being updated' });
    vi.advanceTimersByTime(60_000);
    vi.useRealTimers();

    expect(notices()).toHaveLength(1);
    expect(lines(notices()[0])[0]).toBe('Stopped 1 of 2 experiments: 1 failed');
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
