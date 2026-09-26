// Shared fixtures and helpers for the Builder Beta browser tests.
//
// Specs import `test` and `expect` from here instead of @playwright/test. The
// `builder` fixture opens /builder-beta, records every draft and config the
// test creates, and deletes them afterwards so runs do not pile up drafts on
// the target server.

const crypto = require('crypto');

const base = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

const { attachCapture, fatalOf } = require('./helpers');

const { expect } = base;

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
async function publishTopology(
  request,
  tracker,
  name,
  document = blankDocument(name),
  { sourceToken } = {},
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

  return { draft, documentId: found.id };
}

function isApi(response, method, suffix) {
  return (
    response.request().method() === method &&
    new URL(response.url()).pathname.endsWith(`${API}${suffix}`)
  );
}

// Page object for the Builder Beta view. Methods cover the flows shared by
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

  // The drafts landing's heading. The editor's heading ends in "Builder
  // Flow" too, so the match is exact.
  get landingHeading() {
    return this.page.getByRole('heading', {
      name: 'Builder Flow',
      exact: true,
    });
  }

  async open() {
    await visit(this.page, '/builder-beta');
    await expect(this.landingHeading).toBeVisible({ timeout: 20000 });
  }

  // Creates a blank draft from the landing page and returns {id, owner}.
  async createBlank() {
    const created = this.page.waitForResponse((response) =>
      isApi(response, 'POST', '/builder/drafts'),
    );
    await this.page.getByTestId('drafts-blank').click();
    const draft = await (await created).json();
    await expect(this.canvas).toBeVisible();
    await this.waitSaved();

    return { id: draft.id, owner: draft.owner };
  }

  async waitSaved(timeout = 20000) {
    await expect(this.saveState).toContainText(SAVED, { timeout });
  }

  async rename(title) {
    const name = this.page.getByTestId('builder-name');
    await name.fill(title);
    await name.press('Tab');
  }

  async expectSummary(text) {
    await expect(this.summary).toContainText(text);
  }

  // Connects a device to a switch through the outline's keyboard form. Both
  // arguments are option labels or indexes of the Device and Switch selects.
  async connect(device = { index: 1 }, sw = { index: 1 }) {
    await this.page.locator('#connect-device').selectOption(device);
    await this.page.locator('#connect-switch').selectOption(sw);
    await this.page.getByTestId('outline-connect').click();
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
    // A save still in flight holds the landing back until it lands.
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

// Records server-side objects a test creates so they can be removed.
class Tracker {
  constructor() {
    this.drafts = new Map();
    this.configs = [];
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

const test = base.test.extend({
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
});

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
  BuilderPage,
  SAVED,
  SCHEMA_URI,
  backdropPoint,
  blankDocument,
  draftPath,
  expect,
  expectAccessible,
  expectNoFatal,
  expectNoInvisibleText,
  invisibleText,
  knownDefect,
  publishTopology,
  seedConfig,
  seedDraft,
  test,
  uniqueName,
  visit,
};
