// The Logs page's details dialog: a click or the keyboard on a message opens
// one dialog outside the list, with the entry's level, time, type, text,
// fields and full message; it copies the message, steps through the shown
// entries, and closes on Escape or Close with focus back on the message.
import { flushPromises } from '@vue/test-utils';
import { h } from 'vue';
import { beforeEach, expect, it, vi } from 'vitest';

const { capturing, loaderSpy, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/pageLoader.js', capturing);

import Logs from '@/views/Logs.vue';
import { mountPage, textOf } from './helpers/mount.js';

const AUDIT =
  'experiment started event.kind=event event.type=[start] ' +
  'user.name=cgoes user.roles=[Global Admin] source.ip=10.10.100.226 ' +
  'url.path=/api/v1/experiments/nera/start phenix.duration=1m14.742s';

const LOGS = [
  {
    level: 'INFO',
    type: 'AUDIT',
    timestamp: '2026-11-11 09:00:01.000',
    msg: AUDIT,
  },
  {
    level: 'WARN',
    type: 'SYSTEM',
    timestamp: '2026-11-11 09:00:02.000',
    msg: 'vm restarted msg="host was not ready" vm.name=host-01',
  },
  {
    level: 'ERROR',
    type: 'SECURITY',
    timestamp: '2026-11-11 09:00:03.000',
    msg: 'capture grant revoked',
  },
].map((log, id) => ({ id, ...log }));

// draws every item, as happy-dom gives the virtual scroller no height
const RecycleScroller = {
  props: ['items'],
  methods: { scrollToPosition() {} },
  render() {
    return h(
      'div',
      { class: 'scroller' },
      this.items.map((item, index) => this.$slots.default({ item, index })),
    );
  },
};

async function logsPage() {
  const page = await mountPage(Logs, {
    route: '/log',
    // the real transition, so the modal's root is its own element, as in
    // a browser, for the focus it takes on opening
    stubs: { RecycleScroller, transition: false },
    attachTo: document.body,
  });
  loaderSpy.options.apply([...LOGS]);
  await flushPromises();
  return page;
}

const messageButtons = (page) => page.findAll('.log-message');

// the open dialogs on the page
const openDialogs = () =>
  [...document.querySelectorAll('[role="dialog"]')].filter(
    (el) => el.style.display !== 'none',
  );

// what the open dialog shows
function details() {
  const [dialog] = openDialogs();
  const meta = dialog.querySelector('.log-details-meta');
  return {
    level: textOf(meta.querySelector('.level-tag')),
    levelClass: [...meta.querySelector('.level-tag').classList].find(
      (c) => c.startsWith('level-') && c !== 'level-tag',
    ),
    time: textOf(meta.querySelector('.log-details-time')),
    type: textOf(meta.querySelector('.log-details-type')),
    text: textOf(dialog.querySelector('.log-details-text') ?? ''),
    fields: [...dialog.querySelectorAll('dl > div')].map((row) => [
      row.querySelector('dt').textContent,
      row.querySelector('dd').textContent,
    ]),
    raw: dialog.querySelector('.log-details-raw').textContent,
  };
}

async function openRow(page, i) {
  const button = messageButtons(page).at(i);
  button.element.focus();
  await button.trigger('click');
  await flushPromises();
  return button.element;
}

const dialogButton = (label) =>
  [...openDialogs()[0].querySelectorAll('button')].find(
    (b) => textOf(b) === label,
  );

let writeText;
beforeEach(() => {
  resetMocks();
  writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  });
});

// Main bug: the full message opened in a tooltip that the list cut off.
it('opens one named, modal dialog outside the list on a click, and focuses it', async () => {
  const page = await logsPage();
  expect(openDialogs()).toHaveLength(0);

  await openRow(page, 0);

  const [dialog] = openDialogs();
  expect(dialog.getAttribute('aria-label')).toBe('Log message details');
  expect(dialog.getAttribute('aria-modal')).toBe('true');
  expect(page.find('.scroller').element.contains(dialog)).toBe(false);
  expect(dialog.contains(document.activeElement)).toBe(true);
});

it('opens from the keyboard: each message is a button that Enter and Space click', async () => {
  const page = await logsPage();
  const button = messageButtons(page).at(1);
  expect(button.element.tagName).toBe('BUTTON');
  expect(button.attributes('type')).toBe('button');
  expect(button.attributes('tabindex')).toBeUndefined();
  expect(button.attributes('aria-haspopup')).toBe('dialog');

  button.element.focus();
  // the browser clicks a focused button on Enter or Space
  button.element.click();
  await flushPromises();
  expect(openDialogs()).toHaveLength(1);
  expect(details().text).toBe('vm restarted');
});

it('shows the level, time, type, text and each field in order', async () => {
  const page = await logsPage();
  await openRow(page, 0);

  expect(details()).toEqual({
    level: 'INFO',
    levelClass: 'level-info',
    time: '2026-11-11 09:00:01.000',
    type: 'AUDIT',
    text: 'experiment started',
    fields: [
      ['event.kind', 'event'],
      ['event.type', '[start]'],
      ['user.name', 'cgoes'],
      ['user.roles', '[Global Admin]'],
      ['source.ip', '10.10.100.226'],
      ['url.path', '/api/v1/experiments/nera/start'],
      ['phenix.duration', '1m14.742s'],
    ],
    raw: AUDIT,
  });
  expect(openDialogs()[0].querySelector('.log-details-raw').tabIndex).toBe(0);
});

it('copies the full message and says so in a live region', async () => {
  const page = await logsPage();
  await openRow(page, 0);
  const status = openDialogs()[0].querySelector('.log-details-status');
  expect(status.getAttribute('aria-live')).toBe('polite');
  expect(textOf(status)).toBe('');

  dialogButton('Copy').click();
  await flushPromises();

  expect(writeText).toHaveBeenCalledWith(AUDIT);
  expect(textOf(status)).toBe('Copied');
});

it('selects the full message for the user to copy when the browser refuses', async () => {
  writeText.mockRejectedValue(new Error('NotAllowedError'));
  const page = await logsPage();
  await openRow(page, 0);

  dialogButton('Copy').click();
  await flushPromises();

  const raw = openDialogs()[0].querySelector('.log-details-raw');
  expect(document.getSelection().toString()).toBe(AUDIT);
  expect(document.activeElement).toBe(raw);
  expect(textOf(openDialogs()[0].querySelector('.log-details-status'))).toBe(
    'Could not copy. The message is selected: press Ctrl+C to copy it.',
  );
});

it.each([
  [
    'Escape',
    () => document.dispatchEvent(new KeyboardEvent('keyup', { key: 'Escape' })),
  ],
  ['Close', () => dialogButton('Close').click()],
])('closes on %s, back to the message', async (_, close) => {
  const page = await logsPage();
  const opener = await openRow(page, 1);

  close();
  await flushPromises();

  expect(document.activeElement).toBe(opener);
  // hidden once its closing animation ends
  await vi.waitFor(() => expect(openDialogs()).toHaveLength(0));
});

it('keeps one dialog however many messages are opened', async () => {
  const page = await logsPage();
  for (const i of [0, 1, 2]) {
    await openRow(page, i);
    expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
    expect(details().raw).toBe(LOGS[i].msg);
    // the last message has no fields, so only its text and no field list
    expect(openDialogs()[0].querySelector('dl') !== null).toBe(i < 2);
    if (i === 2) expect(details().text).toBe(LOGS[2].msg);
    dialogButton('Close').click();
    await flushPromises();
  }
});

it('steps to the entries before and after it in the list shown', async () => {
  const page = await logsPage();
  await openRow(page, 1);
  const footer = () => openDialogs()[0].querySelector('.modal-card-foot');
  expect(textOf(footer().querySelector('.log-details-position'))).toBe(
    'Entry 2 of 3',
  );

  dialogButton('Next').click();
  await flushPromises();
  expect(details().raw).toBe(LOGS[2].msg);
  expect(dialogButton('Next').getAttribute('aria-disabled')).toBe('true');
  // the last entry has no next one; focus stays on the button
  dialogButton('Next').click();
  await flushPromises();
  expect(details().raw).toBe(LOGS[2].msg);

  dialogButton('Previous').click();
  dialogButton('Previous').click();
  await flushPromises();
  expect(details().raw).toBe(LOGS[0].msg);
  expect(dialogButton('Previous').getAttribute('aria-disabled')).toBe('true');
});
