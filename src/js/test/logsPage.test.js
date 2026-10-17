// The Logs page: it subscribes to log lines while open, filters them by type,
// and finds AUDIT events by the ECS fields in their message.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { h } from 'vue';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const { capturing, loaderSpy, modules, resetMocks, ws } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/pageData.js', async (original) => ({
  ...(await original()),
  pageFetchers: { logs: vi.fn() },
}));
vi.mock('@/utils/pageLoader.js', capturing);

import Logs from '@/views/Logs.vue';
import { wsMsg } from './helpers/fixtures.js';
import { mountPage, textOf } from './helpers/mount.js';

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
