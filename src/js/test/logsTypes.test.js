// Audit events are log lines of type AUDIT whose ECS fields are part of the
// message, so the Logs page filters them by type and finds them by field.
import { beforeEach, expect, it, vi } from 'vitest';

const { capturing, modules, resetMocks } = await vi.hoisted(
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

async function logsPage() {
  const ctx = pageContext(Logs);
  await Logs.created.call(ctx);
  ctx.logs = LOGS;
  ctx.logsVersion++;
  return ctx;
}

beforeEach(() => resetMocks());

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
