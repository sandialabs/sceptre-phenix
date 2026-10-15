// The server sends log lines only to websocket clients that subscribed, so the
// Logs page must subscribe while open, again after a reconnect, and stop when
// it closes.
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

const actions = () =>
  ws.sent
    .filter((msg) => msg.resource.type === 'log')
    .map((msg) => msg.resource.action);

beforeEach(() => resetMocks());

it('subscribes to log lines while open, and again after a reconnect', async () => {
  const ctx = pageContext(Logs);
  await Logs.created.call(ctx);

  expect(actions()).toEqual(['subscribe']);
  expect(ws.handlers).toEqual([ctx.handleWs]);

  ws.reconnect.forEach((f) => f());
  expect(actions()).toEqual(['subscribe', 'subscribe']);

  Logs.beforeUnmount.call(ctx);
  expect(actions()).toEqual(['subscribe', 'subscribe', 'unsubscribe']);
  expect(ws.reconnect).toHaveLength(0);
  expect(ws.handlers).toHaveLength(0);
});
