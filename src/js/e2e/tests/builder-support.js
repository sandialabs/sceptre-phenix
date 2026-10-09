// Shared fixtures and helpers for the Builder browser tests.
//
// Specs import `test` and `expect` from here instead of @playwright/test;
// this `expect` also has toHaveAnnounced, for what the live region says. The
// `builder` fixture opens /builder, records every draft and config the
// test creates, and deletes them afterwards so runs do not pile up drafts on
// the target server. The `sharingUsers` fixture signs in several users of a
// server with authentication on, each in a browser of their own (see
// builder-sharing.spec.js).

const crypto = require('crypto');
const zlib = require('zlib');

const base = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

const { attachCapture, fatalOf } = require('./helpers');

// --- announcements ----------------------------------------------------------
//
// The live region shows one message until the next replaces it, after a hold
// (announce.js) that some specs shorten to 100 ms. On a slow machine the next
// message can replace one before an assertion on the region reads it, so
// specs do not read the region: each page logs every message its live region
// shows (recordAnnouncements), and expect(builder).toHaveAnnounced(text)
// waits for one in the log.

// Runs in the page, before its own scripts. Each message is a new element in
// the region (BuilderLiveRegion.vue), so a message shown again is logged
// again. The log belongs to the document: a reload starts a new one.
function recordAnnouncements() {
  const log = [];
  window.__e2eAnnounced = { id: Math.random().toString(36).slice(2), log };
  new MutationObserver((records) => {
    for (const { target, addedNodes } of records) {
      if (
        addedNodes.length === 0 ||
        !target.closest?.('[data-testid="builder-live-region"]')
      ) {
        continue;
      }
      for (const node of addedNodes) {
        const text = node.textContent.replace(/\s+/g, ' ').trim();
        if (text) {
          log.push(text);
        }
      }
    }
  }).observe(document, { childList: true, subtree: true });
}

// Runs in the page: where `text` (or the RegExp `source`) is first found in
// the log at or after `from`, and the messages from there on.
function findAnnouncement({ from, text, exact, source, flags }) {
  const { id, log } = window.__e2eAnnounced || { id: '', log: [] };
  const start = from?.id === id ? from : { id, index: 0, offset: 0 };
  const pattern =
    source === undefined
      ? null
      : new RegExp(source, `${flags.replace(/[gy]/g, '')}g`);

  for (let index = start.index; index < log.length; index += 1) {
    const message = log[index];
    const offset = index === start.index ? start.offset : 0;
    let end;

    if (pattern) {
      pattern.lastIndex = offset;
      const match = pattern.exec(message);
      end = match ? match.index + match[0].length : -1;
    } else if (exact) {
      end = offset === 0 && message === text ? message.length : -1;
    } else {
      const at = message.indexOf(text, offset);
      end = at < 0 ? -1 : at + text.length;
    }

    if (end >= 0) {
      return {
        found: { id, index, offset: end },
        shown: log.slice(start.index),
      };
    }
  }

  return { found: null, shown: log.slice(start.index) };
}

// How long toHaveAnnounced waits between looks at the log, in ms.
const ANNOUNCED_POLL_MS = [20, 50, 100, 250];

// For each page, where its next toHaveAnnounced looks from: just after the
// text the last one found. An announcement expected after another must come
// after it, and a message expected twice must be shown twice.
const announcedFrom = new WeakMap();

const expect = base.expect.extend({
  // expect(builder or page).toHaveAnnounced(expected, { exact, timeout })
  // waits until the live region has shown a message containing `expected`,
  // equal to it with exact: true, or matching it when it is a RegExp. The
  // hold joins messages that arrive during it into one message ("Added
  // switch. Added note."). With .not, the log is checked once, without
  // waiting.
  async toHaveAnnounced(target, expected, options = {}) {
    const page = target instanceof BuilderPage ? target.page : target;
    const { exact = false, timeout = this.timeout } = options;
    const query =
      expected instanceof RegExp
        ? { source: expected.source, flags: expected.flags }
        : { text: expected, exact };
    const from = announcedFrom.get(page) || null;
    const deadline = Date.now() + timeout;
    let result = { found: null, shown: [] };

    for (let attempt = 0; ; attempt += 1) {
      try {
        result = await page.evaluate(findAnnouncement, { from, ...query });
      } catch (error) {
        // A navigation replaced the document during the call.
        if (page.isClosed()) {
          throw error;
        }
      }
      if (result.found || this.isNot || Date.now() >= deadline) {
        break;
      }
      await new Promise((resolve) => {
        setTimeout(
          resolve,
          ANNOUNCED_POLL_MS[Math.min(attempt, ANNOUNCED_POLL_MS.length - 1)],
        );
      });
    }

    const pass = result.found !== null;
    if (pass && !this.isNot) {
      announcedFrom.set(page, result.found);
    }

    let kind = 'containing';
    if (expected instanceof RegExp) {
      kind = 'matching';
    } else if (exact) {
      kind = 'equal to';
    }

    return {
      name: 'toHaveAnnounced',
      pass,
      expected,
      actual: result.shown,
      message: () =>
        [
          this.utils.matcherHint('toHaveAnnounced', undefined, undefined, {
            isNot: this.isNot,
          }),
          '',
          `Expected: ${this.isNot ? 'no' : 'a'} message ${kind} ${this.utils.printExpected(expected)}`,
          `Shown since the last announcement found:${result.shown.length ? '' : ' nothing'}`,
          ...result.shown.map((text) => `  ${this.utils.printReceived(text)}`),
          ...(pass || this.isNot ? [] : ['', `Waited ${timeout} ms.`]),
        ].join('\n'),
    };
  },
});

const API = '/api/v1';
const SAVED = 'All changes saved';
const SCHEMA_URI = 'https://phenix.sandia.gov/schemas/builder/v1';

function uniqueName(testInfo, suffix) {
  const nonce = `${Date.now()}-${Math.floor(Math.random() * 100000)}`;

  return `builder-${testInfo.project.name}-${suffix}-${nonce}`;
}

// Opens a route without the fixed pause of helpers.gotoSeeded(). The first
// load of a fresh session no longer redirects home when auth is disabled; if
// it ever does, the route is loaded a second time once the app has mounted.
async function visit(page, path) {
  await page.goto(path);
  await page.waitForFunction(
    () => (document.querySelector('#app')?.childElementCount || 0) > 0,
  );

  const want = new URL(path, page.url()).pathname;
  if (new URL(page.url()).pathname !== want) {
    await page.goto(path);
  }
}

// A test that asserts the correct behavior of a defect found in review. The
// test is expected to fail until the defect is fixed; Playwright then reports
// it as "unexpectedly passed" so the marker can be removed.
//
// Tag such a test `@known-defect` in its declaration, e.g.
// test('…', { tag: '@known-defect' }, async …). The tag puts it in the
// known-defects project, which runs it once, in Chromium, with a short
// assertion timeout.
function knownDefect(summary) {
  const info = base.test.info();
  if (!info.tags.includes('@known-defect')) {
    throw new Error(
      `Tag "${info.title}" with { tag: '@known-defect' } (see builder-support.js)`,
    );
  }

  info.annotations.push({ type: 'known-defect', description: summary });
  base.test.fail(true, `Known defect: ${summary}`);
}

// axe reports text whose color equals its background as "incomplete" rather
// than as a violation, so white-on-white labels pass an axe scan. Fail on any
// visible text in the Builder whose color matches its effective background.
async function invisibleText(page, scope = '.builder-root') {
  return page.evaluate((selector) => {
    const root = document.querySelector(selector);
    if (!root) {
      return [];
    }

    const transparent = (value) =>
      value === 'transparent' || /rgba\(.*,\s*0\)$/.test(value);

    const background = (element) => {
      for (let node = element; node; node = node.parentElement) {
        const value = getComputedStyle(node).backgroundColor;
        if (!transparent(value)) {
          return value;
        }
      }

      return 'rgb(255, 255, 255)';
    };

    const found = [];
    for (const element of root.querySelectorAll('*')) {
      const ownText = [...element.childNodes].some(
        (node) => node.nodeType === Node.TEXT_NODE && node.textContent.trim(),
      );
      if (!ownText || !element.checkVisibility?.()) {
        continue;
      }

      const style = getComputedStyle(element);
      if (style.visibility === 'hidden' || Number(style.opacity) === 0) {
        continue;
      }

      if (style.color === background(element)) {
        found.push({
          tag: element.tagName.toLowerCase(),
          text: element.textContent.trim().slice(0, 60),
          color: style.color,
        });
      }
    }

    return found;
  }, scope);
}

async function expectNoInvisibleText(page, scope) {
  const found = await invisibleText(page, scope);
  expect(found, JSON.stringify(found, null, 2)).toEqual([]);
}

// The WCAG contrast ratio of each element's `property` color, its text color
// by default, with the first opaque background behind it, as {text, color,
// background, ratio}. A border or stroke is judged against what surrounds
// the element.
async function contrast(locator, property = 'color') {
  return locator.evaluateAll((elements, prop) => {
    const transparent = (value) =>
      value === 'transparent' || /rgba\(.*,\s*0\)$/.test(value);

    const behind = (element) => {
      for (let node = element; node; node = node.parentElement) {
        const value = getComputedStyle(node).backgroundColor;
        if (!transparent(value)) {
          return value;
        }
      }

      return 'rgb(255, 255, 255)';
    };

    const luminance = (value) => {
      const [r, g, b] = (value.match(/[\d.]+/g) || []).map(Number);
      const channel = (c) => {
        const v = c / 255;

        return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
      };

      return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
    };

    return elements.map((element) => {
      const color = getComputedStyle(element)[prop];
      const background = behind(
        prop === 'color' ? element : element.parentElement || element,
      );
      const [hi, lo] = [luminance(color), luminance(background)].sort(
        (a, b) => b - a,
      );

      return {
        text: element.textContent.trim().slice(0, 30),
        color,
        background,
        ratio: Math.round(((hi + 0.05) / (lo + 0.05)) * 100) / 100,
      };
    });
  }, property);
}

// Fails on serious or critical axe violations. With `soft`, the failure is
// recorded and the test goes on, so one surface does not hide the next;
// `label` names the surface in the failure message.
async function expectAccessible(
  page,
  { include = '.builder-root', soft = false, label = '' } = {},
) {
  const results = await new AxeBuilder({ page })
    .include(include)
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .analyze();
  const serious = results.violations.filter((item) =>
    ['serious', 'critical'].includes(item.impact),
  );

  const message = [label, JSON.stringify(serious, null, 2)]
    .filter(Boolean)
    .join(': ');
  (soft ? expect.soft : expect)(serious, message).toEqual([]);
}

function draftPath({ owner, id }) {
  return `${API}/builder/drafts/${encodeURIComponent(owner)}/${encodeURIComponent(id)}`;
}

// A Builder document with no nodes, or with the given nodes, networks and
// edges.
function blankDocument(name, { nodes = [], networks = [], edges = [] } = {}) {
  return {
    $schema: SCHEMA_URI,
    revision: 1,
    id: crypto.randomUUID(),
    name,
    nodes,
    networks,
    edges,
    viewport: { x: 0, y: 0, zoom: 1 },
    grid: { enabled: true, size: 16, snap: true },
  };
}

// A diagram that publishes: two Server devices, `server` and `server-2`,
// each connected by eth0 to the switch of network EXP. Tests whose subject is
// not drawing the diagram start from this instead of clicking it together.
function labDocument(name) {
  const id = () => crypto.randomUUID();
  const network = { id: id(), name: 'EXP' };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'EXP',
    position: { x: 0, y: 400 },
    switch: { networkId: network.id },
  };
  const devices = ['server', 'server-2'].map((hostname, index) => ({
    id: id(),
    kind: 'device',
    label: hostname,
    position: { x: index * 320, y: 0 },
    device: {
      hostname,
      iconKey: 'linux',
      spec: {
        type: 'VirtualMachine',
        general: { hostname, vm_type: 'kvm' },
        hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
        network: {
          interfaces: [
            { name: 'eth0', proto: 'dhcp', type: 'ethernet', vlan: 'EXP' },
          ],
        },
      },
      interfaces: [{ id: id(), name: 'eth0', index: 0 }],
    },
  }));

  return blankDocument(name, {
    nodes: [...devices, sw],
    networks: [network],
    edges: devices.map((device) => ({
      id: id(),
      sourceNodeId: device.id,
      sourceHandleId: device.device.interfaces[0].id,
      targetNodeId: sw.id,
      networkId: network.id,
    })),
  });
}

// Creates a draft holding `document` through the API and schedules it for
// deletion. The tracker sees only drafts the page creates, so API-created
// drafts are registered here. Returns the draft metadata, including `etag`.
async function seedDraft(
  request,
  tracker,
  document,
  { title = document.name, sourceToken } = {},
) {
  const response = await request.post(`${API}/builder/drafts`, {
    data: { title, document, ...(sourceToken ? { sourceToken } : {}) },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const draft = await response.json();
  tracker.draft(draft);

  return { ...draft, etag: draft.etag || response.headers().etag };
}

// Publishes `document` as topology `name` through the API, the same calls
// the Publish dialog makes, and schedules the draft and topology for
// deletion. Returns the source draft and the published document's id.
//
// The draft that published a diagram is the one its owner edits the
// diagram in, from Configs and from Published Diagrams alike. A test that
// needs the diagram to open as a draft of its own, as it does for anyone
// else, passes `keepDraft: false`: the draft is deleted once it has
// published.
async function publishTopology(
  request,
  tracker,
  name,
  document = blankDocument(name),
  { sourceToken, keepDraft = true } = {},
) {
  const draft = await seedDraft(request, tracker, document, {
    title: name,
    sourceToken,
  });

  tracker.config('Topology', name);
  const published = await request.post(`${draftPath(draft)}/publish`, {
    headers: { 'If-Match': draft.etag },
    data: { mode: 'topology', topology: { name, action: 'create' } },
  });
  expect(published.ok(), await published.text()).toBeTruthy();
  expect((await published.json()).status).toBe('succeeded');

  const listed = await request.get(`${API}/builder/documents`);
  expect(listed.ok(), await listed.text()).toBeTruthy();
  const found = ((await listed.json()).documents || []).find(
    (entry) => entry.target === name,
  );
  expect(found, `published document for ${name}`).toBeTruthy();

  if (!keepDraft) {
    await deleteDraft(request, draftPath(draft));
  }

  return { draft, documentId: found.id };
}

// Whether `response` answers a `method` request for the API path that ends
// in `suffix`, with the query string `search` when that is given.
function isApi(response, method, suffix, search) {
  const url = new URL(response.url());

  return (
    response.request().method() === method &&
    url.pathname.endsWith(`${API}${suffix}`) &&
    (search === undefined || url.search === search)
  );
}

// Resolves with the page's next response of that kind; see isApi().
function waitForApi(page, method, suffix, search) {
  return page.waitForResponse((response) =>
    isApi(response, method, suffix, search),
  );
}

// The header's counts, as read: "1 device, 0 switches, …".
function summaryText({
  devices = 0,
  switches = 0,
  networks = 0,
  links = 0,
  groups = 0,
  notes = 0,
} = {}) {
  const count = (n, noun, plural = `${noun}s`) =>
    `${n} ${n === 1 ? noun : plural}`;

  return [
    count(devices, 'device'),
    count(switches, 'switch', 'switches'),
    count(networks, 'network'),
    count(links, 'connection'),
    count(groups, 'group'),
    count(notes, 'note'),
  ].join(', ');
}

function devicesOf(document) {
  return (document.nodes || []).filter((node) => node.kind === 'device');
}

async function openConfigs(page) {
  await visit(page, '/configs/');
  await expect(page.locator('table')).toBeVisible({ timeout: 20000 });
}

// A time as a Builder document writes it: UTC, in whole seconds.
const DOCUMENT_TIME = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/;

// The four fields the server writes into a document when it stores it: who
// made the diagram and when, and who saved it last and when.
function provenanceOf(document) {
  const { author, createdAt, updatedBy, updatedAt } = document || {};

  return { author, createdAt, updatedBy, updatedAt };
}

// Expects the row `id` of the Inspector's Details block on `page`
// ('created' or 'edited') to name `user` and to hold the time `at`, as the
// document writes it. The text of the time is in the viewer's locale, so it
// is read from the element's datetime.
async function expectDetail(page, id, user, at, { soft = true } = {}) {
  const row = page.getByTestId(`inspector-${id}`);
  const check = expect.configure({ soft });
  const by = user.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

  await check(row.locator('time'), `${id} time`).toHaveAttribute(
    'datetime',
    at,
  );
  await check(row.locator('dd'), `${id} user`).toHaveText(
    new RegExp(` by ${by}$`),
  );
}

// The server writes a document's times in whole seconds, so a save in the
// second of the one before it has the same time. Resolves once a second has
// passed since `since`, the Date.now() at which the earlier save was
// answered: the next save then has a later time.
async function nextSecond(since) {
  await expect
    .poll(() => Date.now() - since, { timeout: 5000 })
    .toBeGreaterThanOrEqual(1000);
}

// Page object for the Builder view. Methods cover the flows shared by
// several specs; specs use raw locators for anything specific to them.
class BuilderPage {
  constructor(page, request, tracker) {
    this.page = page;
    this.request = request;
    this.tracker = tracker;
  }

  get canvas() {
    return this.page.getByTestId('builder-canvas');
  }

  get summary() {
    return this.page.getByTestId('builder-summary');
  }

  get saveState() {
    return this.page.getByTestId('builder-save-state');
  }

  get liveRegion() {
    return this.page.getByTestId('builder-live-region');
  }

  // Every message the live region has shown since the page loaded, oldest
  // first (see toHaveAnnounced).
  announced() {
    return this.page.evaluate(() => window.__e2eAnnounced.log);
  }

  get inspector() {
    return this.page.locator('section[aria-labelledby="inspector-title"]');
  }

  get outline() {
    return this.page.getByTestId('builder-outline');
  }

  get dialog() {
    return this.page.getByRole('dialog');
  }

  toolbar(action) {
    return this.page.getByTestId(`toolbar-${action}`);
  }

  palette(item) {
    return this.page.getByTestId(`palette-${item}`);
  }

  nodes(kind) {
    const all = this.page.getByTestId('builder-node');

    return kind
      ? all.and(this.page.locator(`[data-node-kind="${kind}"]`))
      : all;
  }

  node(label, kind) {
    return this.nodes(kind).filter({ hasText: label }).first();
  }

  outlineItem(label) {
    return this.page
      .locator('[data-testid^="outline-item-"]')
      .filter({ hasText: label })
      .first();
  }

  // The drafts landing's heading. The editor's heading ends in "Builder"
  // too, so the match is exact.
  get landingHeading() {
    return this.page.getByRole('heading', {
      name: 'Builder',
      exact: true,
    });
  }

  async open() {
    await visit(this.page, '/builder');
    await expect(this.landingHeading).toBeVisible({ timeout: 20000 });
  }

  // Creates a blank draft from the landing page and returns {id, owner}.
  async createBlank() {
    const created = waitForApi(this.page, 'POST', '/builder/drafts');
    await this.page.getByTestId('drafts-blank').click();
    const draft = await (await created).json();
    await expect(this.canvas).toBeVisible();
    await this.waitSaved();

    return { id: draft.id, owner: draft.owner };
  }

  async waitSaved(timeout = 20000) {
    await expect(this.saveState).toContainText(SAVED, { timeout });
  }

  // Renames the diagram through the header's pencil and name field.
  async rename(title) {
    await this.page.getByTestId('builder-name-edit').click();
    const field = this.page.getByTestId('builder-name-field');
    await field.fill(title);
    await field.press('Enter');
  }

  async expectSummary(text) {
    await expect(this.summary).toContainText(text);
  }

  // Expects the header's counts to be `counts` (see summaryText()), any
  // not given 0.
  async expectCounts(counts, { soft = false } = {}) {
    await expect
      .configure({ soft })(this.summary)
      .toHaveText(summaryText(counts));
  }

  // Polls the server's copy of `draft` until `read(document)` equals
  // `expected`. A document without the part `read` looks at yet (a node
  // still being saved) is not there yet rather than a failure. With `soft`,
  // a mismatch is recorded and the test goes on.
  async persisted(
    draft,
    read,
    expected,
    { soft = false, timeout = 20000, message } = {},
  ) {
    await expect
      .configure({ soft })
      .poll(
        async () => {
          const doc = await this.serverDocument(draft);

          try {
            return read(doc);
          } catch {
            return undefined;
          }
        },
        { timeout, message },
      )
      .toEqual(expected);
  }

  // Resolves with the response to the next snapshot upload, whatever its
  // status. Only use it once earlier edits are saved, or it can resolve for
  // one of them.
  nextSnapshot() {
    return this.page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        /\/builder\/drafts\/[^/]+\/[^/]+\/snapshots$/.test(
          new URL(response.url()).pathname,
        ),
    );
  }

  // Connects a device to a switch through the toolbar's Add a connection
  // dialog. Both arguments are option labels or indexes of the Device and
  // Switch selects. The dialog closes once the store has connected them.
  async connect(device = { index: 1 }, sw = { index: 1 }) {
    const dialog = this.page.getByTestId('connect-dialog');

    await this.toolbar('connect').click();
    await expect(dialog).toBeVisible();
    await dialog.locator('#connect-device').selectOption(device);
    await dialog.locator('#connect-switch').selectOption(sw);
    await dialog.getByTestId('connect-dialog-submit').click();
    await expect(dialog).toHaveCount(0);
  }

  // Selects the row alone. Enter on a row that is already the only thing
  // selected deselects it, so that row is left as it is.
  async selectInOutline(label) {
    const item = this.outlineItem(label);
    await item.focus();
    const pressed = this.page.locator(
      '[data-testid^="outline-item-"][aria-pressed="true"]',
    );
    const alone =
      (await item.getAttribute('aria-pressed')) === 'true' &&
      (await pressed.count()) === 1 &&
      (await this.page.locator('.vue-flow__edge.selected').count()) === 0;
    if (!alone) {
      await item.press('Enter');
    }
    await expect(item).toHaveAttribute('aria-pressed', 'true');
  }

  async apply() {
    await this.page.getByTestId('inspector-apply').click();
  }

  async openDialog(action) {
    await this.toolbar(action).click();
    await expect(this.dialog).toBeVisible();

    return this.dialog;
  }

  async backToDrafts() {
    await this.page.getByRole('button', { name: 'Back to drafts' }).click();
    // A save still in flight goes on behind the landing, which the draft's
    // card says; the lists may take a moment to read.
    await expect(this.landingHeading).toBeVisible({ timeout: 20000 });
  }

  // Returns the persisted draft (metadata plus current document) from the
  // server. Call waitSaved() first so the latest edit has been uploaded.
  async serverDraft({ id, owner }) {
    const response = await this.request.get(
      `${API}/builder/drafts/${encodeURIComponent(owner)}/${encodeURIComponent(id)}`,
    );
    expect(response.ok(), await response.text()).toBeTruthy();

    return response.json();
  }

  async serverDocument(draft) {
    const body = await this.serverDraft(draft);

    return typeof body.document === 'string'
      ? JSON.parse(body.document)
      : body.document;
  }

  async config(kind, name) {
    const response = await this.request.get(`${API}/configs/${kind}/${name}`, {
      headers: { Accept: 'application/json' },
    });

    return response.ok() ? response.json() : null;
  }

  // Creates a config through the API and schedules it for deletion.
  async seedConfig(config) {
    await seedConfig(this.request, this.tracker, config);
  }

  // Creates a draft through the API; see seedDraft().
  async seedDraft(document, options) {
    return seedDraft(this.request, this.tracker, document, options);
  }

  // Mounts the editor, which reads the server's source lists, and opens a
  // draft from the landing page.
  async openDraft(draft) {
    await this.open();
    await this.page.getByTestId(`draft-open-${draft.id}`).click();
    await expect(this.canvas).toBeVisible();
    await this.waitSaved();
  }
}

// Creates a config through the API and schedules it for deletion. POST
// /configs answers 201 with an empty body, so there is nothing to return.
async function seedConfig(request, tracker, config) {
  const response = await request.post(`${API}/configs`, { data: config });
  expect(response.ok(), await response.text()).toBeTruthy();
  tracker.config(config.kind, config.metadata.name);
}

// The template library, a user's own: with authentication off every test is
// the one user, so every test reads and changes the same library. A test
// changes and deletes only the templates and collections it made, and never
// the five built-in templates the library starts with.
const LIBRARY = `${API}/builder/templates`;
const LIBRARY_ADDS = /\/builder\/templates\/([^/]+)\/(items|collections)$/;
const BUILTIN_TEMPLATE_IDS = [
  'server',
  'workstation',
  'router',
  'firewall',
  'external',
];

// Sends a change of a library. Every worker's tests change the one record,
// which the server answers with 503 when it keeps changing under a change:
// that is sent again.
async function libraryChange(request, method, path, options = {}) {
  let response;

  for (let attempt = 0; attempt < 8; attempt += 1) {
    response = await request[method](path, options);
    if (response.status() !== 503) {
      break;
    }

    await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
  }

  return response;
}

// Reads the library of the user `request` is: owner, templates, collections
// and icons.
async function readLibrary(request) {
  const response = await request.get(LIBRARY);
  expect(response.ok(), await response.text()).toBeTruthy();

  return response.json();
}

// Adds templates to the library through the API, with `collection` ({name,
// description?}) a collection that holds them, and schedules what it made
// for deletion. Returns the library's owner, the ids of the new templates,
// in order, and the id of the collection.
async function seedTemplates(
  request,
  tracker,
  templates,
  { collection, icons } = {},
) {
  const { owner } = await readLibrary(request);
  const response = await libraryChange(
    request,
    'post',
    `${LIBRARY}/${encodeURIComponent(owner)}/items`,
    {
      data: {
        templates,
        ...(collection ? { collection } : {}),
        ...(icons ? { icons } : {}),
      },
    },
  );
  expect(response.status(), await response.text()).toBe(201);

  const made = await response.json();
  const ids = made.created.map((entry) => entry.id);

  ids.forEach((id) => tracker.template(owner, id));
  if (made.collection) {
    tracker.collection(owner, made.collection.id);
  }

  return { owner, ids, collection: made.collection?.id || '' };
}

// The Node Templates tab of the drafts page, its cards and the row above
// them. Another user's card is named by its owner and id (`theirs`), since
// every library starts with the same built-in ids.
function templateLibrary(page) {
  const by = (id) => page.getByTestId(id);
  const theirs = (prefix) => (owner, id) => by(`${prefix}-${owner}-${id}`);

  return {
    tab: by('drafts-tab-templates'),
    panel: page.locator('#panel-templates'),
    newTemplate: by('templates-new'),
    newCollection: by('collections-new'),
    show: by('templates-show'),
    list: by('templates-list'),
    empty: by('templates-empty'),
    error: by('templates-error'),
    retry: by('templates-retry'),
    card: (id) => by(`template-card-${id}`),
    select: (id) => by(`template-select-${id}`),
    edit: (id) => by(`template-edit-${id}`),
    share: (id) => by(`template-share-${id}`),
    remove: (id) => by(`template-delete-${id}`),
    theirCard: theirs('template-card'),
    theirSelect: theirs('template-select'),
    owner: theirs('template-owner'),
    view: theirs('template-view'),
    copy: theirs('template-copy'),
    bar: by('bulk-bar-templates'),
    all: by('bulk-all-templates'),
    count: by('bulk-count-templates'),
    collect: by('bulk-collect-templates'),
    collectItem: (id) => by(`bulk-collect-templates-item-${id}`),
    uncollect: by('bulk-uncollect-templates'),
    bulkShare: by('bulk-share-templates'),
    bulkDelete: by('bulk-delete-templates'),
    bulkCopy: by('bulk-copy-templates'),
    bulkUnpublish: by('bulk-unpublish-templates'),
    block: by('collection-block'),
    editCollection: by('collection-edit'),
    shareCollection: by('collection-share'),
    deleteCollection: by('collection-delete'),
    copyCollection: by('collection-copy'),
    unpublishCollection: by('collection-unpublish'),
    dialog: {
      root: by('collection-dialog'),
      name: by('collection-name'),
      nameHint: by('collection-name-hint'),
      description: by('collection-description'),
      members: by('collection-members'),
      error: by('collection-error'),
      save: by('collection-save'),
      cancel: by('collection-cancel'),
    },
    confirm: by('builder-confirm'),
    accept: by('confirm-accept'),
    cancel: by('confirm-cancel'),
  };
}

// Records server-side objects a test creates so they can be removed.
class Tracker {
  constructor() {
    this.drafts = new Map();
    this.configs = [];
    // The templates and collections added to a library, by its owner.
    this.libraries = new Map();
  }

  library(owner) {
    if (!this.libraries.has(owner)) {
      this.libraries.set(owner, {
        templates: new Set(),
        collections: new Set(),
      });
    }

    return this.libraries.get(owner);
  }

  template(owner, id) {
    this.library(owner).templates.add(id);
  }

  collection(owner, id) {
    this.library(owner).collections.add(id);
  }

  draft(body) {
    if (body && body.id && body.owner) {
      this.drafts.set(`${body.owner}/${body.id}`, {
        id: body.id,
        owner: body.owner,
      });
    }
  }

  config(kind, name) {
    this.configs.push({ kind, name });
  }

  watch(page) {
    page.on('response', async (response) => {
      const created =
        isApi(response, 'POST', '/builder/drafts') ||
        isApi(response, 'POST', '/builder/generate');
      if (!created || !response.ok()) {
        return;
      }

      try {
        const body = await response.json();
        this.draft(body.draft || body);
      } catch {
        // Response bodies are unavailable once the page has closed.
      }
    });

    // What the page adds to a template library.
    page.on('response', async (response) => {
      const added =
        response.request().method() === 'POST' &&
        response.status() === 201 &&
        new URL(response.url()).pathname.match(LIBRARY_ADDS);
      if (!added) {
        return;
      }

      const owner = decodeURIComponent(added[1]);

      try {
        const body = await response.json();

        if (added[2] === 'collections') {
          this.collection(owner, body.id);
        } else {
          (body.created || []).forEach(({ id }) => this.template(owner, id));
          if (body.collection) {
            this.collection(owner, body.collection.id);
          }
        }
      } catch {
        // Response bodies are unavailable once the page has closed.
      }
    });
  }

  async cleanup(request) {
    const order = { Experiment: 0, Topology: 1, Scenario: 2 };
    const configs = [...this.configs].sort(
      (a, b) => (order[a.kind] ?? 3) - (order[b.kind] ?? 3),
    );
    for (const { kind, name } of configs) {
      await request.delete(`${API}/configs/${kind}/${name}`).catch(() => {});
    }

    for (const { id, owner } of this.drafts.values()) {
      await deleteDraft(request, draftPath({ owner, id }));
    }

    // Ids the library no longer holds are ignored.
    for (const [owner, { templates, collections }] of this.libraries) {
      await libraryChange(
        request,
        'post',
        `${LIBRARY}/${encodeURIComponent(owner)}/delete`,
        {
          data: {
            ...(templates.size ? { templates: [...templates] } : {}),
            ...(collections.size ? { collections: [...collections] } : {}),
          },
        },
      ).catch(() => {});
    }
  }
}

// Deletes a draft with If-Match. An autosave still in flight when the test
// ends changes the ETag after it was read, so a 412 re-reads it and retries.
async function deleteDraft(request, path) {
  for (let attempt = 0; attempt < 5; attempt += 1) {
    const current = await request.get(path).catch(() => null);
    if (!current || !current.ok()) {
      return;
    }

    const etag = current.headers().etag;
    const deleted = await request
      .delete(path, { headers: etag ? { 'If-Match': etag } : {} })
      .catch(() => null);
    if (!deleted || deleted.status() !== 412) {
      return;
    }

    await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
  }
}

// --- Several users (a server with authentication on) -----------------------

const ADMIN_USER = process.env.E2E_ADMIN_USER || 'e2e-admin';
const ADMIN_PASS = process.env.E2E_ADMIN_PASS || 'Testpass1!';
const USER_PASS = 'Testpass1!';
// The users each sharing test signs in, by part.
const SHARING_PARTS = ['owner', 'editor', 'viewer', 'stranger'];

async function signIn(request, user, pass) {
  const response = await request.post(`${API}/login`, {
    data: { user, pass },
  });
  expect(response.ok(), await response.text()).toBeTruthy();

  return response.json();
}

// An API client that sends `token`, as the UI does.
function signedClient(playwright, baseURL, token) {
  return playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { 'X-Phenix-Auth-Token': `bearer ${token}` },
  });
}

// Deletes every draft `username` owns, through their API client.
async function deleteOwnDrafts(api, username) {
  const listed = await api.get(`${API}/builder/drafts`).catch(() => null);
  if (!listed || !listed.ok()) {
    return;
  }

  const body = await listed.json();
  for (const draft of [...(body.drafts || []), ...(body.damaged || [])]) {
    if (draft.owner === username) {
      await deleteDraft(api, draftPath(draft));
    }
  }
}

// Deletes the templates and collections of the library `api`'s user owns,
// once it was changed, so nothing of it stays shared or server-wide: a
// library outlives its user, and other tests list what is server-wide.
async function emptyOwnLibrary(api) {
  const listed = await api.get(LIBRARY).catch(() => null);
  if (!listed || !listed.ok()) {
    return;
  }

  const { owner, templates = [], collections = [] } = await listed.json();
  const own = (items) =>
    items.filter((item) => item.source === 'own').map((item) => item.id);
  const changed =
    collections.some((item) => item.source === 'own') ||
    templates.some((item) => item.source === 'own' && item.created);

  if (owner && changed) {
    await libraryChange(
      api,
      'post',
      `${LIBRARY}/${encodeURIComponent(owner)}/delete`,
      {
        data: {
          ...(own(templates).length ? { templates: own(templates) } : {}),
          ...(own(collections).length ? { collections: own(collections) } : {}),
        },
      },
    ).catch(() => {});
  }
}

// Signs in as the administrator, who then makes the roles and users a test
// asks for (users are named `<part>-<nonce>`), each user signed in with a
// browser context of their own (the session as a sign-in leaves it) and an
// API client. dispose() deletes what the users made (drafts, and their
// template library's items), the users and the roles.
async function userMaker({ browser, playwright }, testInfo) {
  const { baseURL, viewport } = testInfo.project.use;
  const nonce = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;
  const guest = await playwright.request.newContext({ baseURL });
  const admin = await signedClient(
    playwright,
    baseURL,
    (await signIn(guest, ADMIN_USER, ADMIN_PASS)).token,
  );
  const roles = [];
  const users = [];

  return {
    nonce,
    admin,

    // Makes a role from a Role config: an object, or the text of a YAML
    // file. Returns its config name.
    async role(config) {
      const yaml = typeof config === 'string';
      const created = await admin.post(`${API}/configs`, {
        ...(yaml
          ? {
              headers: { 'Content-Type': 'application/x-yaml' },
              data: config,
            }
          : { data: config }),
      });
      expect(created.ok(), await created.text()).toBeTruthy();

      const name = yaml
        ? config.match(/^ {2}name: (\S+)$/m)[1]
        : config.metadata.name;
      roles.push(name);

      return name;
    },

    // Makes a user of the role named `roleName` (its roleName), and signs
    // them in.
    async user(part, roleName) {
      const username = `${part}-${nonce}`;
      const made = await admin.post(`${API}/users`, {
        data: {
          username,
          password: USER_PASS,
          first_name: part,
          last_name: 'E2E',
          role_name: roleName,
          resource_names: ['*'],
        },
      });
      expect(made.ok(), await made.text()).toBeTruthy();

      const session = await signIn(guest, username, USER_PASS);
      const context = await browser.newContext({ baseURL, viewport });
      await context.addInitScript(recordAnnouncements);
      await context.addInitScript((signedIn) => {
        sessionStorage.setItem('phenix.user', signedIn.user.username);
        sessionStorage.setItem('phenix.token', signedIn.token);
        sessionStorage.setItem(
          'phenix.role',
          JSON.stringify(signedIn.user.role),
        );
        sessionStorage.setItem('phenix.auth', 'true');
      }, session);
      const page = await context.newPage();
      const issues = [];
      attachCapture(page, issues);

      const user = {
        username,
        context,
        page,
        issues,
        api: await signedClient(playwright, baseURL, session.token),
      };
      users.push(user);

      return user;
    },

    async dispose() {
      for (const user of users) {
        await deleteOwnDrafts(user.api, user.username);
        await emptyOwnLibrary(user.api);
        await user.context.close().catch(() => {});
        await user.api.dispose();
      }
      for (const { username } of users) {
        await admin.delete(`${API}/users/${username}`).catch(() => {});
      }
      for (const name of roles) {
        await admin.delete(`${API}/configs/Role/${name}`).catch(() => {});
      }
      await admin.dispose();
      await guest.dispose();
    },
  };
}

// The role of the sharing tests' users: it may read and change configs, by
// any name, and so make, change, publish and delete drafts of its own, and
// read the schemas and disks the editor offers; it may not see other users'
// drafts (no builder-drafts), list users, or publish templates server-wide.
function authorRole(nonce) {
  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Role',
    metadata: { name: `e2e-builder-author-${nonce}` },
    spec: {
      roleName: `E2E Builder Author ${nonce}`,
      policies: [
        {
          // "*/*" names a config in full, such as Topology/site, which
          // publishing a topology and opening its diagram are checked by.
          resources: ['configs', 'configs/*'],
          resourceNames: ['*', '*/*'],
          verbs: ['list', 'get', 'create', 'update', 'delete'],
        },
        { resources: ['schemas'], resourceNames: ['*'], verbs: ['get'] },
        { resources: ['disks'], resourceNames: ['*'], verbs: ['list'] },
      ],
    },
  };
}

// One user of authorRole for each of SHARING_PARTS, named for the test.
// Everything is deleted afterwards.
async function sharingUsers({ browser, playwright }, use, testInfo) {
  const maker = await userMaker({ browser, playwright }, testInfo);
  const users = {};

  try {
    const role = authorRole(maker.nonce);
    await maker.role(role);
    for (const part of SHARING_PARTS) {
      users[part] = await maker.user(part, role.spec.roleName);
    }

    await use(users);
  } finally {
    await maker.dispose();
  }
}

const test = base.test.extend({
  // How long the live region holds each message, in ms, when set: the
  // product's hold (announce.js) when not. A spec whose tests do not test
  // what screen readers hear, or how messages are paced, may shorten it
  // with test.use({ announceHold: 100 }); messages that follow each other
  // are then shown one by one, where the product's hold joins them.
  announceHold: [undefined, { option: true }],

  // Every page of the test logs what its live region shows (see
  // toHaveAnnounced).
  context: async ({ context }, use) => {
    await context.addInitScript(recordAnnouncements);
    await use(context);
  },

  page: async ({ page, announceHold }, use) => {
    if (announceHold !== undefined) {
      await page.addInitScript((hold) => {
        window.__BUILDER_ANNOUNCE_HOLD_MS__ = hold;
      }, announceHold);
    }
    await use(page);
  },

  // Console, page-error and HTTP issues seen by the page.
  issues: async ({ page }, use) => {
    const issues = [];
    attachCapture(page, issues);
    await use(issues);
  },

  tracker: async ({ page, request }, use) => {
    const tracker = new Tracker();
    tracker.watch(page);
    await use(tracker);
    await tracker.cleanup(request);
  },

  builder: async ({ page, request, tracker }, use) => {
    await use(new BuilderPage(page, request, tracker));
  },

  sharingUsers,

  // Makes the roles and users a test asks for, on a server with
  // authentication on, and deletes them afterwards (see userMaker).
  userMaker: async ({ browser, playwright }, use, testInfo) => {
    const maker = await userMaker({ browser, playwright }, testInfo);

    try {
      await use(maker);
    } finally {
      await maker.dispose();
    }
  },
});

// --- custom icons -------------------------------------------------------------

// What starts every PNG file.
const PNG_SIGNATURE = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);

// A PNG of one color ([red, green, blue]), built here rather than read from
// a fixture file: each test draws its own colors, so its bytes are its own.
function pngOf(width, height, color) {
  const chunk = (kind, data = Buffer.alloc(0)) => {
    const body = Buffer.concat([Buffer.from(kind, 'latin1'), data]);
    const length = Buffer.alloc(4);
    const checksum = Buffer.alloc(4);

    length.writeUInt32BE(data.length);
    checksum.writeUInt32BE(zlib.crc32(body));

    return Buffer.concat([length, body, checksum]);
  };
  const header = Buffer.alloc(13);

  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  // Eight bits a channel, with alpha.
  header.set([8, 6, 0, 0, 0], 8);

  const row = Buffer.concat([
    Buffer.from([0]),
    Buffer.alloc(width * 4, Buffer.from([...color, 255])),
  ]);
  const pixels = Buffer.alloc(row.length * height, row);

  return Buffer.concat([
    PNG_SIGNATURE,
    chunk('IHDR', header),
    chunk('IDAT', zlib.deflateSync(pixels)),
    chunk('IEND'),
  ]);
}

// An icon as a document carries it: its id, the digest of its bytes, and
// its entry.
function iconOf(bytes, name) {
  const id = `sha256:${crypto.createHash('sha256').update(bytes).digest('hex')}`;
  const data = bytes.toString('base64');

  return { id, data, entry: name ? { name, data } : { data } };
}

// A color of the test's own: no theme color, and neither black, white nor
// a grey.
function ownColor() {
  const color = [0, 0, 0].map(() => 48 + crypto.randomInt(160));

  return Math.max(...color) - Math.min(...color) < 24 ? ownColor() : color;
}

// Fails the test when the page logged a JavaScript error.
function expectNoFatal(issues) {
  const fatal = fatalOf(issues);
  expect(fatal, JSON.stringify(fatal, null, 2)).toHaveLength(0);
}

// A point on the backdrop of the open modal `dialog`: left of it, level with
// its middle.
async function backdropPoint(dialog) {
  const box = await dialog.boundingBox();

  return { x: Math.max(1, box.x / 2), y: box.y + box.height / 2 };
}

module.exports = {
  ADMIN_PASS,
  ADMIN_USER,
  API,
  BUILTIN_TEMPLATE_IDS,
  BuilderPage,
  DOCUMENT_TIME,
  LIBRARY,
  PNG_SIGNATURE,
  SAVED,
  SCHEMA_URI,
  authorRole,
  backdropPoint,
  blankDocument,
  contrast,
  devicesOf,
  draftPath,
  expect,
  expectAccessible,
  expectDetail,
  expectNoFatal,
  expectNoInvisibleText,
  iconOf,
  invisibleText,
  isApi,
  knownDefect,
  labDocument,
  libraryChange,
  nextSecond,
  openConfigs,
  ownColor,
  pngOf,
  provenanceOf,
  publishTopology,
  readLibrary,
  recordAnnouncements,
  seedConfig,
  seedDraft,
  seedTemplates,
  signIn,
  summaryText,
  templateLibrary,
  test,
  uniqueName,
  USER_PASS,
  visit,
  waitForApi,
};
