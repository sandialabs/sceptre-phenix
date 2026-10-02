// The server sends log lines only to websocket clients that subscribed, so the
// Logs page must subscribe while open, again after a reconnect, and stop when
// it closes.
import { beforeEach, expect, it, vi } from 'vitest';

const ws = vi.hoisted(() => ({
  sent: [],
  reconnect: [],
  addWsHandler: vi.fn(),
  removeWsHandler: vi.fn(),
}));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: ws.addWsHandler,
  removeWsHandler: ws.removeWsHandler,
  sendWsMsg: (msg) => ws.sent.push(msg),
  onWsReconnect: (f) => {
    ws.reconnect.push(f);
    return () => ws.reconnect.splice(ws.reconnect.indexOf(f), 1);
  },
}));

vi.mock('@/utils/axios.js', () => ({ default: { get: vi.fn() } }));
vi.mock('@/utils/pageData.js', () => ({
  DEFAULT_LOG_WINDOW: 600,
  pageFetchers: { logs: vi.fn() },
}));
vi.mock('@/utils/pageLoader.js', () => ({
  createPageLoader: () => ({ start: () => {}, stop: () => {}, load: vi.fn() }),
  loadingText: (what) => `Loading ${what}…`,
}));

import Logs from '@/views/Logs.vue';
import { makeContext } from './helpers/context.js';

const actions = () =>
  ws.sent
    .filter((msg) => msg.resource.type === 'log')
    .map((msg) => msg.resource.action);

beforeEach(() => {
  ws.sent.length = 0;
  ws.reconnect.length = 0;
});

it('subscribes to log lines while open, and again after a reconnect', async () => {
  const ctx = makeContext(Logs);
  await Logs.created.call(ctx);

  expect(actions()).toEqual(['subscribe']);
  expect(ws.addWsHandler).toHaveBeenCalledWith(ctx.handleWs);

  ws.reconnect.forEach((f) => f());
  expect(actions()).toEqual(['subscribe', 'subscribe']);

  Logs.beforeUnmount.call(ctx);
  expect(actions()).toEqual(['subscribe', 'subscribe', 'unsubscribe']);
  expect(ws.reconnect).toHaveLength(0);
});
