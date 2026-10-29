// The Logs page: it subscribes to log lines while open, filters them by type,
// finds AUDIT events by the ECS fields in their message, shows each level as
// a short badge, counts the lines shown, and links to the log files.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { h } from 'vue';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, rbac, resetMocks, ws } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/pageData.js', async (original) => ({
  ...(await original()),
  pageFetchers: { logs: vi.fn() },
}));
vi.mock('@/utils/pageLoader.js', capturing);

import Logs from '@/views/Logs.vue';
import { wsMsg } from './helpers/fixtures.js';
import { mountPage, textOf, tooltipButtons } from './helpers/mount.js';
import { componentDeclarations } from './helpers/css.js';
import { allowOnly, denyAll } from './helpers/permissions.js';

const LOGS = [
  {
    level: 'INFO',
    type: 'AUDIT',
    msg: 'deleted disk event.action=disk-delete event.outcome=success user.name=alice',
  },
  {
    level: 'WARN',
    type: 'AUDIT',
    msg: 'starting capture for VM not allowed event.action=capture-start event.outcome=failure user.name=bob',
  },
  { level: 'INFO', type: 'SYSTEM', msg: 'disk-delete mentioned elsewhere' },
  { level: 'WARN', type: 'SECURITY', msg: 'capture grant revoked' },
].map((log, id) => ({ id, ...log }));

// draws every item, as happy-dom gives the virtual scroller no height
const RecycleScroller = {
  props: ['items'],
  methods: { scrollToPosition() {} },
  render() {
    return h(
      'div',
      this.items.map((item, index) => this.$slots.default({ item, index })),
    );
  },
};

const open = () =>
  mountPage(Logs, { route: '/log', stubs: { RecycleScroller } });

// the page once its loader has sent it LOGS
async function logsPage() {
  const page = await open();
  loaderSpy.options.apply([...LOGS]);
  await flushPromises();
  return page;
}

// the message of each log line shown
const shown = (page) => page.findAll('.truncate').map(textOf);

// the item of the type dropdown with this text
const typeItem = (page, type) =>
  page
    .findAll('.dropdown')
    .at(2)
    .findAll('.dropdown-item')
    .find((item) => textOf(item) === type);

// the item of the level dropdown with this text
const levelItem = (page, level) =>
  page
    .findAll('.dropdown')
    .at(1)
    .findAll('.dropdown-item')
    .find((item) => textOf(item) === level);

// the "Logs shown" count, as "label: count"
const shownCount = (page) => {
  const stat = page.find('[aria-label="Logs shown"] .stat');
  return `${textOf(stat.find('dt'))}: ${textOf(stat.find('dd'))}`;
};

// each log line's level badge: its text and color class
const badges = (page) =>
  page
    .findAll('.level-tag')
    .map((tag) => [
      textOf(tag),
      tag.classes().find((c) => c.startsWith('level-') && c !== 'level-tag'),
    ]);

const searchBox = (page) =>
  page.find('input[aria-label="Search log messages"]');

const actions = () =>
  ws.sent
    .filter((msg) => msg.resource.type === 'log')
    .map((msg) => msg.resource.action);

beforeEach(() => resetMocks());
afterEach(() => vi.useRealTimers());

// the server sends log lines only to websocket clients that subscribed
it('subscribes to log lines while open, and again after a reconnect', async () => {
  const page = await logsPage();

  expect(actions()).toEqual(['subscribe']);
  const line = { level: 'INFO', type: 'SYSTEM', msg: 'streamed line' };
  for (const handler of ws.handlers)
    handler(wsMsg('log', 'phenix', 'log', line));
  await flushPromises();
  expect(shown(page).at(-1)).toBe('streamed line');

  ws.reconnect.forEach((f) => f());
  expect(actions()).toEqual(['subscribe', 'subscribe']);

  page.unmount();
  expect(actions()).toEqual(['subscribe', 'subscribe', 'unsubscribe']);
  expect(ws.reconnect).toHaveLength(0);
  expect(ws.handlers).toHaveLength(0);
});

it('offers AUDIT among the log types', async () => {
  const page = await logsPage();

  expect(typeItem(page, 'AUDIT')).toBeDefined();
});

it.each([
  ['the AUDIT type', ['AUDIT'], '', [0, 1]],
  ['an event.action', [], 'event.action=disk-delete', [0]],
  ['an outcome within the AUDIT type', ['AUDIT'], 'event.outcome=failure', [1]],
  ['other types', ['SYSTEM', 'SECURITY'], '', [2, 3]],
])('filters by %s', async (_, types, search, want) => {
  const page = await logsPage();
  for (const type of types) await typeItem(page, type).trigger('click');
  await searchBox(page).setValue(search);

  // the search applies once typing pauses
  await vi.waitFor(() =>
    expect(shown(page)).toEqual(want.map((i) => LOGS[i].msg)),
  );
});

const LEVELS = ['DEBUG', 'INFO', 'WARN', 'ERROR'];
const TYPES = ['AUDIT', 'SYSTEM', 'SECURITY'];
const logArb = fc.record({
  level: fc.constantFrom(...LEVELS),
  type: fc.constantFrom(...TYPES),
  msg: fc
    .array(fc.constantFrom('disk', 'Disk', 'vm', 'outcome=failure'), {
      minLength: 1,
      maxLength: 3,
    })
    .map((words) => words.join(' ')),
});

it('shows the lines that pass the filters, as lines stream in and filters change', async () => {
  // a step: a streamed line, a type toggled, a level or a search chosen
  const stepArb = fc.oneof(
    logArb.map((line) => ({ line })),
    fc.constantFrom(...TYPES).map((type) => ({ type })),
    fc.constantFrom(...LEVELS).map((level) => ({ level })),
    fc
      .constantFrom('', 'disk', 'DISK VM', 'failure')
      .map((search) => ({ search })),
  );
  await fc.assert(
    fc.asyncProperty(
      fc.array(logArb, { maxLength: 4 }),
      fc.array(stepArb, { minLength: 1, maxLength: 8 }),
      async (first, steps) => {
        vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
        resetMocks();
        const page = await open();
        loaderSpy.options.apply(
          first.map((log, i) => ({ id: -1 - i, ...log })),
        );
        await flushPromises();

        const logs = [...first];
        const types = new Set();
        let level = 'INFO';
        let search = '';
        for (const step of steps) {
          if (step.line) {
            logs.push(step.line);
            for (const handler of ws.handlers)
              handler(wsMsg('log', 'phenix', 'log', step.line));
          } else if (step.type) {
            if (!types.delete(step.type)) types.add(step.type);
            await typeItem(page, step.type).trigger('click');
          } else if (step.level) {
            level = step.level;
            await levelItem(page, level).trigger('click');
          } else {
            search = step.search;
            await searchBox(page).setValue(search);
            vi.advanceTimersByTime(200);
          }
          await flushPromises();

          expect(shownCount(page)).toMatch(
            new RegExp(
              `^Logs shown: ${shown(page).length}( of ${logs.length})?$`,
            ),
          );
          expect(shown(page)).toEqual(
            logs
              .filter(
                (log) =>
                  LEVELS.indexOf(log.level) >= LEVELS.indexOf(level) &&
                  (types.size === 0 || types.has(log.type)) &&
                  log.msg.toLowerCase().includes(search.toLowerCase()),
              )
              .map((log) => log.msg),
          );
        }
        page.unmount();
      },
    ),
    { numRuns: 20 },
  );
});

it('shows each level as a short colored badge', async () => {
  const page = await open();
  loaderSpy.options.apply(
    LEVELS.map((level, id) => ({
      id,
      level,
      type: 'SYSTEM',
      msg: `a ${level} line`,
    })),
  );
  await levelItem(page, 'DEBUG').trigger('click');
  await flushPromises();

  expect(badges(page)).toEqual([
    ['DBG', 'level-debug'],
    ['INFO', 'level-info'],
    ['WRN', 'level-warn'],
    ['ERR', 'level-error'],
  ]);
});

// happy-dom lays nothing out, so this reads the rules that keep the columns
// fixed: a long unbroken message must not squeeze them or wrap a timestamp
it('keeps every row aligned with the header, however long its message', async () => {
  const style = (selector) =>
    componentDeclarations('src/views/Logs.vue', selector);
  for (const column of ['.level-column', '.ts-column', '.type-column']) {
    expect(style(column)).toMatchObject({ flex: 'none' });
    expect(style(column).width).toBeDefined();
  }
  expect(style('.ts-column')).toMatchObject({ 'white-space': 'nowrap' });
  expect(style('.msg-column')).toMatchObject({ 'min-width': '0' });
  expect(style('.truncate')).toMatchObject({ 'overflow-wrap': 'anywhere' });

  const page = await open();
  loaderSpy.options.apply([
    { id: 0, level: 'INFO', type: 'HTTP', msg: 'x'.repeat(2000) },
    { id: 1, level: 'INFO', type: 'HTTP', msg: 'short' },
  ]);
  await flushPromises();
  const rows = page.findAll('.columns.row');
  expect(rows).toHaveLength(3);
  for (const row of rows) {
    expect(row.findAll('.log-column').map((c) => c.classes()[1])).toEqual([
      'level-column',
      'ts-column',
      'type-column',
      expect.any(String),
    ]);
  }
});

it('counts the lines shown in its box, and of how many when filters hide some', async () => {
  const page = await logsPage();

  expect(shownCount(page)).toBe('Logs shown: 4');
  await typeItem(page, 'AUDIT').trigger('click');
  expect(shownCount(page)).toBe('Logs shown: 2 of 4');

  for (const handler of ws.handlers)
    handler(wsMsg('log', 'phenix', 'log', LOGS[0]));
  await flushPromises();
  expect(shownCount(page)).toBe('Logs shown: 3 of 5');
});

// the download buttons, once the page has asked which log files there are
async function downloadsPage(files) {
  if (files instanceof Error) axios.get.mockRejectedValue(files);
  else axios.get.mockResolvedValue({ data: { files } });
  const page = await open();
  await flushPromises();
  return tooltipButtons(page)
    .filter(({ label }) => label.startsWith('Download'))
    .map(({ tooltip, label, disabled, control }) => ({
      label,
      tooltip,
      disabled,
      href: control.attributes('href') ?? null,
    }));
}

const PHENIX = {
  label: 'Download phēnix log',
  tooltip: 'Download the phēnix log file',
  disabled: false,
  href: '/api/v1/logs/files/phenix?token=t',
};

it.each([
  [
    'both files',
    [
      { name: 'phenix', available: true, size: 10 },
      { name: 'minimega', available: true, size: 20 },
    ],
    [
      PHENIX,
      {
        label: 'Download minimega log',
        tooltip: 'Download the minimega log file',
        disabled: false,
        href: '/api/v1/logs/files/minimega?token=t',
      },
    ],
  ],
  [
    'an unconfigured minimega log',
    [
      { name: 'phenix', available: true, size: 10 },
      { name: 'minimega', available: false, reason: 'not configured' },
    ],
    [
      PHENIX,
      {
        label: 'Download minimega log',
        tooltip: 'Unavailable: not configured',
        disabled: true,
        href: null,
      },
    ],
  ],
  [
    'a failed listing',
    Object.assign(new Error('Network Error'), {
      response: { data: { message: 'forbidden' } },
    }),
    ['phēnix log', 'minimega log'].map((name) => ({
      label: `Download ${name}`,
      tooltip: 'Could not check the log files: forbidden',
      disabled: true,
      href: null,
    })),
  ],
])(
  'links to the log files, or says why not, with %s',
  async (_, files, want) => {
    expect(await downloadsPage(files)).toEqual(want);
    expect(axios.get).toHaveBeenCalledWith('logs/files', expect.anything());
  },
);

// the download buttons' state: whether each one links to its file
const linked = (page) =>
  tooltipButtons(page)
    .filter(({ label }) => label.startsWith('Download'))
    .map(({ control }) => control.attributes('href') !== undefined);

const MISSING = [
  { name: 'phenix', available: true, size: 10 },
  { name: 'minimega', available: false, reason: 'does not exist yet' },
];
const BOTH = [
  { name: 'phenix', available: true, size: 10 },
  { name: 'minimega', available: true, size: 20 },
];

it('asks again which log files there are every 30 seconds and after a reconnect', async () => {
  vi.useFakeTimers();
  axios.get.mockResolvedValue({ data: { files: MISSING } });
  const page = await open();
  await flushPromises();
  expect(linked(page)).toEqual([true, false]);

  axios.get.mockResolvedValue({ data: { files: BOTH } });
  await vi.advanceTimersByTimeAsync(30 * 1000);
  await flushPromises();
  expect(linked(page)).toEqual([true, true]);

  axios.get.mockResolvedValue({ data: { files: MISSING } });
  ws.reconnect.forEach((f) => f());
  await flushPromises();
  expect(linked(page)).toEqual([true, false]);

  page.unmount();
  const calls = axios.get.mock.calls.length;
  await vi.advanceTimersByTimeAsync(60 * 1000);
  expect(axios.get.mock.calls.length).toBe(calls);
});

it('clears a failed listing once one succeeds', async () => {
  vi.useFakeTimers();
  axios.get.mockRejectedValue(new Error('Network Error'));
  const page = await open();
  await flushPromises();
  expect(linked(page)).toEqual([false, false]);

  axios.get.mockResolvedValue({ data: { files: BOTH } });
  await vi.advanceTimersByTimeAsync(30 * 1000);
  await flushPromises();
  expect(linked(page)).toEqual([true, true]);
});

it('does not ask again for log files without logs get', async () => {
  vi.useFakeTimers();
  rbac.allowed = denyAll;
  axios.get.mockResolvedValue({ data: { files: [] } });
  await open();
  await flushPromises();
  const calls = axios.get.mock.calls.length;

  await vi.advanceTimersByTimeAsync(60 * 1000);
  ws.reconnect.forEach((f) => f());
  expect(axios.get.mock.calls.length).toBe(calls);
});

// the buttons end the toolbar at the window's right edge
it('opens the download tooltips to the left, inside the window', async () => {
  axios.get.mockResolvedValue({ data: { files: MISSING } });
  const page = await open();
  await flushPromises();

  const tips = page
    .find('.log-downloads')
    .findAllComponents({ name: 'BTooltip' });
  expect(tips.map((tip) => tip.props('position'))).toEqual([
    'is-left',
    'is-left',
  ]);
});

it('spins the download buttons until it knows which files there are', async () => {
  axios.get.mockReturnValue(new Promise(() => {}));
  const page = await open();

  const downloads = page
    .findAll('.log-downloads button')
    .map((b) => [
      b.classes('is-loading'),
      b.attributes('disabled') !== undefined,
    ]);
  expect(downloads).toEqual([
    [true, true],
    [true, true],
  ]);
});

it.each([
  ['with', allowOnly(['logs', 'get']), 2],
  ['without', denyAll, 0],
])('offers the downloads only %s logs get', async (_, role, want) => {
  rbac.allowed = role;
  axios.get.mockResolvedValue({ data: { files: [] } });
  const page = await open();

  expect(page.findAll('.log-downloads .button')).toHaveLength(want);
});
