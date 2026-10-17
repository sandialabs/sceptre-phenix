// The Logs page: it subscribes to log lines while open, filters them by type,
// and finds AUDIT events by the ECS fields in their message.
import { beforeEach, expect, it, vi } from 'vitest';

const { capturing, modules, resetMocks, ws } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/pageData.js', () => ({
  DEFAULT_LOG_WINDOW: 600,
  pageFetchers: { logs: vi.fn() },
}));
vi.mock('@/utils/pageLoader.js', capturing);

import Logs from '@/views/Logs.vue';
import { pageContext } from './helpers/page.js';

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
];

async function opened() {
  const ctx = pageContext(Logs);
  await Logs.created.call(ctx);
  return ctx;
}

async function logsPage() {
  const ctx = await opened();
  ctx.logs = LOGS;
  ctx.logsVersion++;
  return ctx;
}

const actions = () =>
  ws.sent
    .filter((msg) => msg.resource.type === 'log')
    .map((msg) => msg.resource.action);

beforeEach(() => resetMocks());

// the server sends log lines only to websocket clients that subscribed
it('subscribes to log lines while open, and again after a reconnect', async () => {
  const ctx = await opened();

  expect(actions()).toEqual(['subscribe']);
  expect(ws.handlers).toEqual([ctx.handleWs]);

  ws.reconnect.forEach((f) => f());
  expect(actions()).toEqual(['subscribe', 'subscribe']);

  Logs.beforeUnmount.call(ctx);
  expect(actions()).toEqual(['subscribe', 'subscribe', 'unsubscribe']);
  expect(ws.reconnect).toHaveLength(0);
  expect(ws.handlers).toHaveLength(0);
});

it('offers AUDIT among the log types', async () => {
  const ctx = await logsPage();

  expect(ctx.knownTypes).toContain('AUDIT');
});

it.each([
  ['the AUDIT type', { typeFilter: ['AUDIT'] }, [0, 1]],
  ['an event.action', { searchFilter: 'event.action=disk-delete' }, [0]],
  [
    'an outcome within the AUDIT type',
    { typeFilter: ['AUDIT'], searchFilter: 'event.outcome=failure' },
    [1],
  ],
  ['other types', { typeFilter: ['SYSTEM', 'SECURITY'] }, [2, 3]],
])('filters by %s', async (_, filters, want) => {
  const ctx = await logsPage();
  Object.assign(ctx, filters);

  expect(ctx.filteredLogs).toEqual(want.map((i) => LOGS[i]));
});
