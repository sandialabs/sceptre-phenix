// Shared fixtures and helpers for the Builder browser tests: specs import
// `test` and `expect` (with toHaveAnnounced) from here (see README).

const crypto = require('crypto');
const fs = require('fs');
const zlib = require('zlib');

const base = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

const { attachCapture, fatalOf, isChromium, visit } = require('./helpers');

// --- announcements ----------------------------------------------------------

// Runs in the page: logs each message the live region shows, as the next one
// can replace it before an assertion reads it. A reload starts a new log.
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
// text the last one found.
const announcedFrom = new WeakMap();

const expect = base.expect.extend({
  // Waits until the live region has shown a message containing `expected`
  // (equal with exact: true, or matching a RegExp). With .not it checks once.
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

// Visible text whose color is its background's, which axe reports only as
// "incomplete".
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

// The WCAG contrast ratio of each element's `property` color (text by default)
// with the first opaque background behind it, as {text, color, background, ratio}.
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

// Fails on serious or critical axe violations. `soft` records the failure and
// goes on, and `label` names the surface. Chromium only.
async function expectAccessible(
  page,
  { include = '.builder-root', soft = false, label = '' } = {},
) {
  if (!isChromium(page)) return;
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

function draftPath({ id }) {
  return `${API}/builder/drafts/${encodeURIComponent(id)}`;
}

// A Builder document with the given nodes, networks and edges (none by default).
function blankDocument(name, { nodes = [], networks = [], edges = [] } = {}) {
  return {
    $schema: SCHEMA_URI,
    revision: 1,
    metadata: { id: crypto.randomUUID(), name },
    nodes,
    networks,
    edges,
    viewport: { x: 0, y: 0, zoom: 1 },
    grid: { enabled: true, size: 16 },
  };
}

// A diagram that publishes: a Server device for each of `hostnames`, each
// connected by eth0 to the switch of network EXP.
function labDocument(name, { hostnames = ['server', 'server-2'] } = {}) {
  const id = () => crypto.randomUUID();
  const network = { id: id(), name: 'EXP' };
  const sw = {
    id: id(),
    kind: 'switch',
    label: 'EXP',
    position: { x: 0, y: 400 },
    switch: { networkId: network.id },
  };
  const devices = hostnames.map((hostname, index) => ({
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
// deletion. Returns the draft metadata, including `etag`.
async function seedDraft(
  request,
  tracker,
  document,
  { title = document.metadata.name, sourceToken } = {},
) {
  const response = await request.post(`${API}/builder/drafts`, {
    data: { title, document, ...(sourceToken ? { sourceToken } : {}) },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const draft = await response.json();
  tracker.draft(draft);

  return { ...draft, etag: draft.etag || response.headers().etag };
}

// Publishes `document` as topology `name` through the API and schedules both
// for deletion. `keepDraft: false` deletes the source draft once published.
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
// in `suffix`.
function isApi(response, method, suffix) {
  return (
    response.request().method() === method &&
    new URL(response.url()).pathname.endsWith(`${API}${suffix}`)
  );
}

// Whether a request goes to a draft's publish route: a POST to
// /builder/drafts/{draft}/publish.
function isPublishRoute(request) {
  return (
    request.method() === 'POST' &&
    /\/builder\/drafts\/[^/]+\/publish$/.test(new URL(request.url()).pathname)
  );
}

// Whether a request is a dry run of the publish route (dryRun in the body),
// which the Publish dialog sends to preview what publishing changes.
function isPublishPreview(request) {
  if (!isPublishRoute(request)) {
    return false;
  }

  try {
    return request.postDataJSON()?.dryRun === true;
  } catch {
    return false;
  }
}

// Whether a request publishes a draft: a POST to its publish route that is
// not a dry run (see isPublishPreview).
function isPublishRequest(request) {
  return isPublishRoute(request) && !isPublishPreview(request);
}

// Whether a response answers a publish of a draft. See isPublishRequest().
function isPublishResponse(response) {
  return isPublishRequest(response.request());
}

// Resolves with the page's next response of that kind. See isApi().
function waitForApi(page, method, suffix) {
  return page.waitForResponse((response) => isApi(response, method, suffix));
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

function byHostname(doc, hostname) {
  return devicesOf(doc).find((node) => node.device.hostname === hostname);
}

// Every draft-create request the page sends from now on.
function watchDraftCreates(page) {
  const creates = [];
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname.endsWith(`${API}/builder/drafts`)
    ) {
      creates.push(request.url());
    }
  });

  return creates;
}

// The file `trigger` makes the page download, as {name, path, buffer, text}.
async function download(page, trigger) {
  const [file] = await Promise.all([page.waitForEvent('download'), trigger()]);
  const path = await file.path();
  const buffer = fs.readFileSync(path);

  return {
    name: file.suggestedFilename(),
    path,
    buffer,
    text: buffer.toString('utf8'),
  };
}

// Vue Flow's wrapper of the node `id`: its focusable, named element.
function flowNode(page, id) {
  return page.locator(`.vue-flow__node[data-id="${id}"]`);
}

// The Inspector's group of fields titled `title`.
function specGroup(builder, title) {
  return builder.inspector
    .locator('legend.group-label', { hasText: new RegExp(`^${title}$`) })
    .locator('xpath=..');
}

// JSON Forms text controls commit on change, so leave the field after typing.
async function fillField(field, value) {
  await field.fill(value);
  await field.blur();
}

// Presses Apply with the keyboard: a click right after an edit can miss it as
// the layout shifts.
async function applyEdits(builder) {
  const apply = builder.inspector.getByTestId('inspector-apply');
  await expect(apply).toBeEnabled();
  await apply.press('Enter');
}

function errorBanner(page) {
  return page.getByTestId('builder-error').getByRole('alert');
}

async function openUpload(page) {
  await page.getByTestId('drafts-upload').click();
  const dialog = page.getByRole('dialog', { name: 'Upload diagram' });
  await expect(dialog).toBeVisible();

  return dialog;
}

async function openConfigs(page) {
  await visit(page, '/configs/');
  await expect(page.locator('table')).toBeVisible({ timeout: 20000 });
}

// A time as a Builder document writes it: UTC, in whole seconds.
const DOCUMENT_TIME = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/;

// The four provenance fields the server writes into a document's metadata.
function provenanceOf(document) {
  const { createdBy, createdAt, updatedBy, updatedAt } =
    document?.metadata || {};

  return { createdBy, createdAt, updatedBy, updatedAt };
}

// Expects the Inspector's Details row `id` ('created' or 'edited') to name
// `user` and hold the time `at` (read from its datetime, not the local text).
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

// Resolves once a second has passed since `since`, so the next save has a
// later time: the server writes times in whole seconds.
async function nextSecond(since) {
  await expect
    .poll(() => Date.now() - since, { timeout: 5000 })
    .toBeGreaterThanOrEqual(1000);
}

// Page object for the Builder view. Methods cover the flows shared by
// several specs. Specs use raw locators for anything specific to them.
class BuilderPage {
  constructor(page, request, tracker) {
    this.page = page;
    this.request = request;
    this.tracker = tracker;
  }

  // Sets Builder settings (settings.js) for every page load from now on. Call
  // it before the Builder opens.
  async useSettings(settings) {
    await this.page.addInitScript((wanted) => {
      const key = 'phenix.builder.settings';
      let kept;

      try {
        kept = JSON.parse(localStorage.getItem(key) || '{}') || {};
      } catch {
        kept = {};
      }
      localStorage.setItem(key, JSON.stringify({ ...kept, ...wanted }));
    }, settings);
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

  get dialog() {
    return this.page.getByRole('dialog');
  }

  toolbar(action) {
    return this.page.getByTestId(`toolbar-${action}`);
  }

  // A button of the Inspector's header: connect (Add connection) or regroup
  // (Move to group).
  inspectorButton(action) {
    return this.inspector.getByTestId(`inspector-${action}`);
  }

  // Runs a command by its title from the command palette: Ctrl+K (⌘K) on
  // the canvas, the title, then Enter on the first match.
  async runFromPalette(title) {
    const palette = this.page.getByTestId('commands-dialog');

    await this.canvas.focus();
    await this.page.keyboard.press('ControlOrMeta+k');
    await palette.getByRole('combobox').fill(title);
    await expect(palette.getByRole('option', { selected: true })).toContainText(
      title,
    );
    await this.page.keyboard.press('Enter');
    await expect(palette).toHaveCount(0);
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

  // The first node on the canvas, as Vue Flow's wrapper (a toggle button),
  // whose label or else accessible name is `label`.
  canvasItem(label) {
    const exact = new RegExp(
      `^\\s*${label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*$`,
    );

    const nodes = this.page.locator('#builder-canvas .vue-flow__node');

    // A drawing with no label is named by its kind ("Rectangle").
    return nodes
      .filter({
        has: this.page.locator('.builder-node__label, .builder-line__label', {
          hasText: exact,
        }),
      })
      .or(
        nodes.and(this.page.getByRole('button', { name: label, exact: true })),
      )
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

  // Creates a blank draft from the landing page and returns {id}.
  async createBlank() {
    const created = waitForApi(this.page, 'POST', '/builder/drafts');
    await this.page.getByTestId('drafts-blank').click();
    const draft = await (await created).json();
    await expect(this.canvas).toBeVisible();
    await this.waitSaved();

    return { id: draft.id };
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
  // `expected`. A `read` that throws counts as not there yet.
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
  // status. Call it once earlier edits are saved.
  nextSnapshot() {
    return this.page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        /\/builder\/drafts\/[^/]+\/snapshots$/.test(
          new URL(response.url()).pathname,
        ),
    );
  }

  // Connects a device to a switch through the Add a connection dialog. Each
  // argument is an option label or index of its select.
  async connect(device = { index: 1 }, sw = { index: 1 }) {
    const dialog = this.page.getByTestId('connect-dialog');

    await this.runFromPalette('Add a connection');
    await expect(dialog).toBeVisible();
    await dialog.locator('#connect-device').selectOption(device);
    await dialog.locator('#connect-switch').selectOption(sw);
    await dialog.getByTestId('connect-dialog-submit').click();
    await expect(dialog).toHaveCount(0);
  }

  // Selects the node alone by Enter on its canvas item, unless it is already
  // the only thing selected (Enter would deselect it).
  async selectNode(label) {
    const item = this.canvasItem(label);
    await item.focus();
    const pressed = this.page.locator(
      '#builder-canvas .vue-flow__node[aria-pressed="true"]',
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
    await this.page
      .getByRole('button', { name: 'Drafts', exact: true })
      .click();
    // A save still in flight goes on behind the landing, which the draft's
    // card says. The lists may take a moment to read.
    await expect(this.landingHeading).toBeVisible({ timeout: 20000 });
  }

  // Returns the persisted draft (metadata plus current document) from the
  // server. Call waitSaved() first so the latest edit has been uploaded.
  async serverDraft(draft) {
    const response = await this.request.get(draftPath(draft));
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

  // Creates a draft through the API. See seedDraft().
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

  // Edits `draft`, reloads (or opens the Builder again with via: 'open') and
  // opens it from the landing (see README). Resolves with the dialog types accepted.
  async editAndReload(
    draft,
    {
      edit,
      expectSaved,
      offline,
      expectOnLanding,
      expectAfterReload,
      via = 'reload',
    } = {},
  ) {
    if (edit) {
      await edit(this);
    }
    if (expectSaved) {
      await expectSaved(this);
    }

    const prompts = [];
    const accept = (dialog) => {
      prompts.push(dialog.type());
      dialog.accept().catch(() => {});
    };

    this.page.on('dialog', accept);
    try {
      if (via === 'open') {
        await this.open();
      } else {
        // The address names the open draft, which a reload opens again. A
        // reload of the bare address shows the landing.
        await this.page.evaluate(() =>
          window.history.replaceState(
            window.history.state,
            '',
            window.location.pathname,
          ),
        );
        await this.page.reload();
        await expect(this.landingHeading).toBeVisible({ timeout: 20000 });
      }
    } finally {
      this.page.off('dialog', accept);
    }

    if (offline) {
      await this.page.unroute(offline);
    }
    if (expectOnLanding) {
      await expectOnLanding(this);
    }

    await this.page.getByTestId(`draft-open-${draft.id}`).click();
    await expect(this.canvas).toBeVisible();
    if (expectAfterReload) {
      await expectAfterReload(this);
    }

    return prompts;
  }
}

// Creates a config through the API and schedules it for deletion. POST
// /configs answers 201 with an empty body, so there is nothing to return.
async function seedConfig(request, tracker, config) {
  const response = await request.post(`${API}/configs`, { data: config });
  expect(response.ok(), await response.text()).toBeTruthy();
  tracker.config(config.kind, config.metadata.name);
}

// The template library is the server's, so a test changes only what it made.
// The five built-in templates are read only.
const LIBRARY = `${API}/builder/templates`;
const LIBRARY_ADDS = /\/builder\/templates\/(items|collections)$/;
const BUILTIN_TEMPLATE_IDS = [
  'server',
  'workstation',
  'router',
  'firewall',
  'external',
];

// Sends a change of the library, again after each 503 (busy).
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

// Reads the library: its templates and collections, the built-in templates
// and the server's collections.
async function readLibrary(request) {
  const response = await request.get(LIBRARY);
  expect(response.ok(), await response.text()).toBeTruthy();

  return response.json();
}

// Adds templates (and `collection`, {name, description?}) through the API and
// schedules them for deletion. Returns {ids, collection}.
async function seedTemplates(request, tracker, templates, { collection } = {}) {
  const response = await libraryChange(request, 'post', `${LIBRARY}/items`, {
    data: {
      templates,
      ...(collection ? { collection } : {}),
    },
  });
  expect(response.status(), await response.text()).toBe(201);

  const made = await response.json();
  const ids = made.created.map((entry) => entry.id);

  ids.forEach((id) => tracker.template(id));
  if (made.collection) {
    tracker.collection(made.collection.id);
  }

  return { ids, collection: made.collection?.id || '' };
}

// Replaces a library template through the API with what `change` makes of the
// listed one, as another window would. Returns the template as stored.
async function changeTemplate(request, id, change) {
  const listed = (await readLibrary(request)).templates.find(
    (item) => item.id === id,
  );
  expect(listed, `library template ${id}`).toBeTruthy();

  const { name, description = '', device } = change(listed);
  const response = await libraryChange(
    request,
    'put',
    `${LIBRARY}/items/${encodeURIComponent(id)}`,
    {
      headers: { 'If-Match': listed.etag },
      data: { name, description, device },
    },
  );
  expect(response.status(), await response.text()).toBe(200);

  return response.json();
}

// The controls of the drafts page's Node Templates tab. A read-only card is
// named by its source ("builtin" or "preloaded") and id.
function templateLibrary(page) {
  const by = (id) => page.getByTestId(id);
  const readOnly = (prefix) => (source, id) => by(`${prefix}-${source}-${id}`);

  return {
    tab: by('drafts-tab-templates'),
    panel: page.locator('#panel-templates'),
    newTemplate: by('templates-new'),
    newCollection: by('collections-new'),
    show: by('templates-show'),
    filter: by('templates-filter'),
    filterCount: by('templates-filter-count'),
    list: by('templates-list'),
    empty: by('templates-empty'),
    error: by('templates-error'),
    retry: by('templates-retry'),
    card: (id) => by(`template-card-${id}`),
    select: (id) => by(`template-select-${id}`),
    edit: (id) => by(`template-edit-${id}`),
    remove: (id) => by(`template-delete-${id}`),
    builtinNote: by('templates-builtin-note'),
    missingImages: (id) => by(`template-missing-images-${id}`),
    missingNote: by('templates-missing-note'),
    roCard: readOnly('template-card'),
    roTag: readOnly('template-read-only'),
    view: readOnly('template-view'),
    duplicate: readOnly('template-duplicate'),
    bar: by('bulk-bar-templates'),
    all: by('bulk-all-templates'),
    count: by('bulk-count-templates'),
    collect: by('bulk-collect-templates'),
    collectItem: (id) => by(`bulk-collect-templates-item-${id}`),
    uncollect: by('bulk-uncollect-templates'),
    bulkDelete: by('bulk-delete-templates'),
    block: by('collection-block'),
    editCollection: by('collection-edit'),
    deleteCollection: by('collection-delete'),
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
    this.drafts = new Set();
    this.configs = [];
    // The templates and collections added to the library.
    this.templates = new Set();
    this.collections = new Set();
    // The names of the icons added to the server's icon library.
    this.icons = new Set();
    // The IDs of the sets added to the server's layer set library.
    this.layerSets = new Set();
  }

  layerSet(id) {
    this.layerSets.add(id);
  }

  icon(name) {
    this.icons.add(name);
  }

  template(id) {
    this.templates.add(id);
  }

  collection(id) {
    this.collections.add(id);
  }

  draft(body) {
    if (body && body.id) {
      this.drafts.add(body.id);
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

    // What the page adds to the icon library.
    page.on('response', async (response) => {
      if (
        !isApi(response, 'POST', '/builder/icons') ||
        response.status() !== 201
      ) {
        return;
      }

      try {
        this.icon((await response.json()).name);
      } catch {
        // Response bodies are unavailable once the page has closed.
      }
    });

    // What the page adds to the layer set library.
    page.on('response', async (response) => {
      if (
        !isApi(response, 'POST', '/builder/layersets') ||
        response.status() !== 201
      ) {
        return;
      }

      try {
        this.layerSet((await response.json()).id);
      } catch {
        // Response bodies are unavailable once the page has closed.
      }
    });

    // What the page adds to the template library.
    page.on('response', async (response) => {
      const added =
        response.request().method() === 'POST' &&
        response.status() === 201 &&
        new URL(response.url()).pathname.match(LIBRARY_ADDS);
      if (!added) {
        return;
      }

      try {
        const body = await response.json();

        if (added[1] === 'collections') {
          this.collection(body.id);
        } else {
          (body.created || []).forEach(({ id }) => this.template(id));
          if (body.collection) {
            this.collection(body.collection.id);
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

    for (const id of this.drafts) {
      await deleteDraft(request, draftPath({ id }));
    }

    // Ids the library no longer holds are left out.
    if (this.templates.size || this.collections.size) {
      await deleteLibraryItems(request, {
        templates: [...this.templates],
        collections: [...this.collections],
      }).catch(() => {});
    }

    // Names the library no longer has are answered 404, and ignored.
    for (const name of this.icons) {
      await deleteIcon(request, name).catch(() => {});
    }

    // IDs the library no longer has are answered 404, and ignored.
    for (const id of this.layerSets) {
      await deleteTagged(
        request,
        `${API}/builder/layersets/${encodeURIComponent(id)}`,
      ).catch(() => {});
    }
  }
}

// Deletes what path names at the tag that a read of it answers with. A
// change in between is answered 412: the delete reads the tag again. What
// is gone already is answered 404. Returns the last answer.
async function deleteTagged(request, path) {
  let response;

  for (let attempt = 0; attempt < 5; attempt += 1) {
    const current = await request.get(path);
    if (!current.ok()) {
      return current;
    }

    const { etag } = await current.json();

    response = await request.delete(path, { headers: { 'If-Match': etag } });
    if (response.status() !== 412) {
      break;
    }
  }

  return response;
}

// Routes the icon library to `icons`, each as the server gives one icon
// ({name, id, data, ...}): the list without the images, as the server lists
// it, and each icon with its image. No real icon is touched.
async function routeIconLibrary(page, icons) {
  const listed = icons.map((icon) => {
    const row = { ...icon };

    delete row.data;

    return row;
  });

  await page.route('**/api/v1/builder/icons', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({
          json: { icons: listed, maxIcons: 2000, etag: '"routed"' },
        })
      : route.fallback(),
  );
  await page.route('**/api/v1/builder/icons/*', (route) => {
    const name = decodeURIComponent(
      new URL(route.request().url()).pathname.split('/').pop(),
    ).toLowerCase();
    const icon = icons.find((entry) =>
      [entry.name, ...(entry.aliases || [])].some(
        (each) => each.toLowerCase() === name,
      ),
    );

    return route.request().method() === 'GET' && icon
      ? route.fulfill({ json: icon })
      : route.fallback();
  });
}

// Deletes an icon of the library by its name or an alias, at its tag.
function deleteIcon(request, name) {
  return deleteTagged(request, iconPath(name));
}

// Deletes templates and collections of the library, each at the tag the
// library has of it, in one request. Ids the library no longer holds are
// left out. Another change that comes first is answered 412: the library is
// read again. Returns the last answer, or null when nothing is left.
async function deleteLibraryItems(
  request,
  { templates = [], collections = [] },
) {
  let response = null;

  for (let attempt = 0; attempt < 5; attempt += 1) {
    const read = await request.get(LIBRARY);
    if (!read.ok()) {
      return read;
    }

    const library = await read.json();
    const tagged = (ids, items) =>
      ids.flatMap((id) => {
        const item = (items || []).find((entry) => entry.id === id);

        return item ? [{ id, etag: item.etag }] : [];
      });
    const data = {
      templates: tagged(templates, library.templates),
      collections: tagged(collections, library.collections),
    };

    if (!data.templates.length && !data.collections.length) {
      return null;
    }

    response = await libraryChange(request, 'post', `${LIBRARY}/delete`, {
      data,
    });
    if (response.status() !== 412) {
      break;
    }
  }

  return response;
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
// The built-in role that grants the Builder and the configs it publishes.
const BUILDER_ROLE = 'Builder';

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

// Deletes every draft `username` created, through their API client.
async function deleteCreatedDrafts(api, username) {
  const listed = await api.get(`${API}/builder/drafts`).catch(() => null);
  if (!listed || !listed.ok()) {
    return;
  }

  const body = await listed.json();
  for (const draft of [...(body.drafts || []), ...(body.damaged || [])]) {
    if (draft.createdBy === username) {
      await deleteDraft(api, draftPath(draft));
    }
  }
}

// Signs in as the administrator, who makes Builder users (`<part>-<nonce>`),
// each with a browser context and API client. dispose() deletes them.
async function userMaker({ browser, playwright }, testInfo) {
  const { baseURL, viewport } = testInfo.project.use;
  const nonce = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;
  const guest = await playwright.request.newContext({ baseURL });
  const admin = await signedClient(
    playwright,
    baseURL,
    (await signIn(guest, ADMIN_USER, ADMIN_PASS)).token,
  );
  const users = [];

  return {
    nonce,

    // Makes a user of the built-in Builder role, and signs them in.
    async user(part) {
      const username = `${part}-${nonce}`;
      const made = await admin.post(`${API}/users`, {
        data: {
          username,
          password: USER_PASS,
          first_name: part,
          last_name: 'E2E',
          role_name: BUILDER_ROLE,
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

      const user = {
        username,
        context,
        page,
        api: await signedClient(playwright, baseURL, session.token),
      };
      users.push(user);

      return user;
    },

    async dispose() {
      for (const user of users) {
        await deleteCreatedDrafts(user.api, user.username);
        await user.context.close().catch(() => {});
        await user.api.dispose();
      }
      for (const { username } of users) {
        await admin.delete(`${API}/users/${username}`).catch(() => {});
      }
      await admin.dispose();
      await guest.dispose();
    },
  };
}

const test = base.test.extend({
  // Every page of the test logs what its live region shows (see
  // toHaveAnnounced).
  context: async ({ context }, use) => {
    await context.addInitScript(recordAnnouncements);
    await use(context);
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

  // Makes Builder users on a server with auth on, and deletes them after.
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

// A PNG of one color ([red, green, blue]), so each test's bytes are its own.
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

// A name for an icon of the test's own: the server's icon library is
// shared by every test and every worker, and a name is unique in it.
function iconName(prefix = 'icon') {
  return `${prefix}-${crypto.randomUUID().slice(0, 8)}`;
}

// The route of an icon of the library, by one of its names.
function iconPath(name) {
  return `${API}/builder/icons/${encodeURIComponent(name)}`;
}

// An icon: its name, its id (the digest of its bytes), its data, and its
// entry as a document carries it.
function iconOf(bytes, name = iconName()) {
  const id = `sha256:${crypto.createHash('sha256').update(bytes).digest('hex')}`;
  const data = bytes.toString('base64');

  return { name, id, data, entry: { data } };
}

// Uploads `icon` (see iconOf) to the server's icon library, and schedules
// it for deletion. Returns the icon as the server answers.
async function seedIcon(request, tracker, icon) {
  const response = await request.post(`${API}/builder/icons`, {
    data: { name: icon.name, data: icon.data },
  });
  expect(response.status(), await response.text()).toBe(201);
  tracker.icon(icon.name);

  return response.json();
}

// Adds a set to the server's layer set library through the API, and
// schedules it for deletion. Returns the set as the server answers.
async function seedLayerSet(request, tracker, content) {
  const response = await request.post(`${API}/builder/layersets`, {
    data: content,
  });
  expect(response.status(), await response.text()).toBe(201);

  const set = await response.json();

  tracker.layerSet(set.id);

  return set;
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
  API,
  BUILTIN_TEMPLATE_IDS,
  BuilderPage,
  DOCUMENT_TIME,
  LIBRARY,
  PNG_SIGNATURE,
  SAVED,
  SCHEMA_URI,
  USER_PASS,
  applyEdits,
  backdropPoint,
  blankDocument,
  byHostname,
  changeTemplate,
  contrast,
  deleteIcon,
  deleteLibraryItems,
  devicesOf,
  download,
  draftPath,
  errorBanner,
  expect,
  expectAccessible,
  expectDetail,
  expectNoFatal,
  expectNoInvisibleText,
  fillField,
  flowNode,
  iconName,
  iconOf,
  iconPath,
  invisibleText,
  isPublishPreview,
  isPublishRequest,
  isPublishResponse,
  labDocument,
  libraryChange,
  nextSecond,
  openConfigs,
  openUpload,
  ownColor,
  pngOf,
  provenanceOf,
  publishTopology,
  readLibrary,
  routeIconLibrary,
  seedConfig,
  seedIcon,
  seedLayerSet,
  seedTemplates,
  signIn,
  specGroup,
  summaryText,
  templateLibrary,
  test,
  uniqueName,
  visit,
  waitForApi,
  watchDraftCreates,
};
