// Builder Beta import, export, generate and drafts-landing flows.
//
// Every draft the page creates is removed by the `tracker` fixture. Drafts,
// topologies and experiments this spec creates through the API are registered
// with the tracker too (seedDraft, publishTopology and createExperiment).

const crypto = require('crypto');
const fs = require('fs');

const {
  API,
  SCHEMA_URI,
  backdropPoint,
  blankDocument,
  draftPath,
  expect,
  expectAccessible,
  expectNoFatal,
  knownDefect,
  publishTopology,
  seedConfig,
  test,
  uniqueName,
  visit,
} = require('./builder-support');

const API_VERSION = 'phenix.sandia.gov/v1';

// --- local helpers -----------------------------------------------------------

function isApiCall(response, method, suffix) {
  return (
    response.request().method() === method &&
    new URL(response.url()).pathname.endsWith(`${API}${suffix}`)
  );
}

function waitForApi(page, method, suffix) {
  return page.waitForResponse((response) =>
    isApiCall(response, method, suffix),
  );
}

// What the page sees when the user comes back to its browser tab or window:
// the document becomes visible and the window takes focus.
async function showPageAgain(page) {
  await page.evaluate(() => {
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('focus'));
  });
}

// Records every draft-create request the page sends from now on.
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

// Records every message the page's live region shows, so one that has been
// replaced since is still seen. See recordedAnnouncements.
async function recordAnnouncements(page) {
  await page.getByTestId('builder-live-region').evaluate((region) => {
    window.__e2eAnnouncements = [];
    new MutationObserver(() => {
      const text = region.textContent.trim();
      const seen = window.__e2eAnnouncements;

      if (text && seen[seen.length - 1] !== text) {
        seen.push(text);
      }
    }).observe(region, { childList: true, subtree: true, characterData: true });
  });
}

function recordedAnnouncements(page) {
  return page.evaluate(() => window.__e2eAnnouncements);
}

// A phenix v1 topology node. `interfaces` is a list of [name, vlan] pairs.
function topologyNode(hostname, interfaces, extra = {}) {
  return {
    type: 'VirtualMachine',
    general: { hostname, vm_type: 'kvm', ...extra },
    hardware: {
      os_type: 'linux',
      vcpus: 1,
      memory: 512,
      drives: [{ image: 'miniccc.qc2' }],
    },
    network: {
      interfaces: interfaces.map(([name, vlan], index) => ({
        name,
        vlan,
        address: `10.${index}.0.${10 + hostname.length}`,
        mask: 24,
        proto: 'static',
        type: 'ethernet',
      })),
    },
  };
}

function topologyConfig(name, nodes) {
  return {
    apiVersion: API_VERSION,
    kind: 'Topology',
    metadata: { name },
    spec: { nodes },
  };
}

// host-a and host-b share VLAN EXP; host-b also sits on MGMT.
function sharedVlanNodes() {
  return [
    topologyNode('host-a', [['eth0', 'EXP']]),
    topologyNode('host-b', [
      ['eth0', 'EXP'],
      ['eth1', 'MGMT'],
    ]),
  ];
}

// Publishes a one-device (pub-host), one-switch diagram as topology `name`,
// generated from an uploaded config the way Generate builds it. Returns the
// topology name, the source draft and the published document's id.
async function publishDiagram(request, tracker, name) {
  const generated = await request.post(`${API}/builder/generate`, {
    data: {
      content: JSON.stringify(
        topologyConfig(name, [topologyNode('pub-host', [['eth0', 'EXP']])]),
      ),
    },
  });
  expect(generated.ok(), await generated.text()).toBeTruthy();
  const { document } = await generated.json();

  const published = await publishTopology(request, tracker, name, document, {
    sourceToken: `uploaded/Topology/${name}`,
  });

  return { name, ...published };
}

// Creates an experiment from a stored topology, or skips the test when this
// phenix server cannot create experiments.
async function createExperiment(request, tracker, name, topology) {
  const created = await request.post(`${API}/experiments`, {
    data: { name, topology },
  });
  tracker.config('Experiment', name);
  test.skip(
    !created.ok(),
    `Experiment creation is unavailable on this server: ${created.status()} ${await created.text()}`,
  );

  return created;
}

// Rewrites a stored experiment's VLAN aliases through the configs API.
async function setExperimentAliases(request, name, aliases) {
  const current = await request.get(`${API}/configs/Experiment/${name}`, {
    headers: { Accept: 'application/json' },
  });
  expect(current.ok(), await current.text()).toBeTruthy();

  const config = await current.json();
  config.spec.vlans = { ...(config.spec.vlans || {}), aliases };

  const updated = await request.put(`${API}/configs/Experiment/${name}`, {
    data: config,
  });
  expect(updated.ok(), await updated.text()).toBeTruthy();
}

// Waits until the server copy of `draft` passes `check`, then until the page
// reports it saved. An edit is written to IndexedDB before the upload starts,
// so for a moment after an edit the save state still reads "All changes
// saved" from the previous save; waitSaved() alone can return too early.
async function waitPersisted(builder, draft, check) {
  await expect
    .poll(async () => check(await builder.serverDocument(draft)), {
      message: 'the latest edit reaches the server',
    })
    .toBe(true);
  await builder.waitSaved();
}

// Vue Flow's wrapper around the one canvas node of `kind`: its Tab stop.
function flowNode(builder, kind) {
  return builder.page
    .locator('.vue-flow__node')
    .filter({ has: builder.nodes(kind) });
}

// Builds `EXP` switch + `node` + one connection in a fresh blank draft. The
// device sits below and to the right of the switch, so the line leaves the
// device's right side and runs round, left of the switch, the leftmost node,
// into the switch's left side.
async function buildConnectedDiagram(builder, title) {
  const draft = await builder.createBlank();
  await builder.rename(title);
  await builder.palette('switch').click();
  await builder.palette('device').click();
  // A new node is selected, and arrow keys move it 10px.
  const device = flowNode(builder, 'device');
  await expect(device).toBeVisible();
  await device.focus();
  for (let step = 0; step < 16; step += 1) {
    await builder.page.keyboard.press('ArrowDown');
  }
  await builder.connect();
  await builder.expectSummary('1 device, 1 switch, 1 network, 1 connection');
  await expect(builder.page.getByTestId('builder-name')).toHaveValue(title);
  await waitPersisted(builder, draft, (doc) => {
    const [sw, node] = ['switch', 'device'].map((kind) =>
      doc.nodes.find((item) => item.kind === kind),
    );

    return (
      doc.name === title &&
      doc.nodes.length === 2 &&
      doc.edges.length === 1 &&
      node.position.y - sw.position.y === 160
    );
  });

  return draft;
}

// Width and height from a PNG's IHDR chunk.
function pngSize(buffer) {
  return { width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20) };
}

// The diagram of buildConnectedDiagram() as the canvas draws it: the
// connection's stroke and width, points on the line in flow coordinates, and
// the device's fill with a point inside the device clear of its text. The
// points on the line are 3px outside the device's right border and the
// switch's left border, at the heights where the line ends, and the middle
// of the line's run left of the switch. A line that meets its nodes passes
// through the first two; its label hides the middle of the line.
async function diagramOnCanvas(page) {
  return page.locator('.vue-flow__viewport').evaluate((viewport) => {
    const path = viewport.querySelector('path.builder-edge');
    const style = getComputedStyle(path);
    const length = path.getTotalLength();
    const along = Array.from({ length: Math.floor(length) + 1 }, (_, at) =>
      path.getPointAtLength(at),
    );
    const lineEnds = [along[0], path.getPointAtLength(length)];
    const nodeBox = (kind) => {
      const node = viewport
        .querySelector(`[data-node-kind="${kind}"]`)
        .closest('.vue-flow__node');
      const [x, y] = node.style.transform.match(/-?[\d.]+/g).map(Number);

      return { node, x, y, width: node.offsetWidth, height: node.offsetHeight };
    };
    const beside = (kind, side) => {
      const { x, width } = nodeBox(kind);
      const border = side === 'right' ? x + width : x;
      const outside = side === 'right' ? border + 3 : border - 3;
      const end = lineEnds.reduce((best, point) =>
        Math.abs(point.x - border) < Math.abs(best.x - border) ? point : best,
      );

      return [outside, end.y];
    };
    const furthestLeft = Math.min(...along.map((point) => point.x));
    const leftRun = along.filter((point) => point.x < furthestLeft + 0.5);
    const left = leftRun[Math.floor(leftRun.length / 2)];
    const device = nodeBox('device');

    return {
      stroke: style.stroke,
      width: style.strokeWidth,
      line: {
        'beside the device': beside('device', 'right'),
        'beside the switch': beside('switch', 'left'),
        'left of the switch': [left.x, left.y],
      },
      leftOfNodes: left.x < nodeBox('switch').x && left.x < device.x,
      deviceFill: getComputedStyle(device.node.querySelector('.builder-node'))
        .backgroundColor,
      deviceInside: [
        device.x + device.width - 12,
        device.y + device.height / 2,
      ],
    };
  });
}

// Soft-checks an image export, as the browser draws it, against `diagram`
// (from diagramOnCanvas): the line is there in its colour, still meets its
// nodes with the handles left out, and runs left of the switch; and the
// device has its plain fill, not the selected one. `kind` is 'PNG' or
// 'SVG'; `origin` is the flow point at the image's top-left corner.
async function expectDiagramInImage(
  page,
  kind,
  buffer,
  diagram,
  { origin, boundsWidth },
) {
  const probes = [
    ...Object.entries(diagram.line).map(([name, point]) => [
      `${kind} line colour ${name}`,
      point,
      diagram.stroke,
    ]),
    [`${kind} device fill`, diagram.deviceInside, diagram.deviceFill],
  ];
  const type = { PNG: 'image/png', SVG: 'image/svg+xml' }[kind];
  const distances = await page.evaluate(
    async ({ data, type, probes, origin, boundsWidth }) => {
      const image = new Image();
      image.src = `data:${type};base64,${data}`;
      await image.decode();
      const canvas = document.createElement('canvas');
      canvas.width = image.width;
      canvas.height = image.height;
      const context = canvas.getContext('2d');
      context.drawImage(image, 0, 0);
      const scale = image.width / boundsWidth;

      // The closest colour to the wanted one within 3px of each point, as
      // the largest channel difference.
      return probes.map(([, [x, y], colour]) => {
        const want = colour.match(/\d+/g).slice(0, 3).map(Number);
        const { data: pixels } = context.getImageData(
          Math.round((x - origin.x) * scale) - 3,
          Math.round((y - origin.y) * scale) - 3,
          7,
          7,
        );
        let best = 255;
        for (let i = 0; i < pixels.length; i += 4) {
          const channels = want.map((value, k) =>
            Math.abs(pixels[i + k] - value),
          );
          best = Math.min(best, Math.max(...channels));
        }
        return best;
      });
    },
    { data: buffer.toString('base64'), type, probes, origin, boundsWidth },
  );
  for (const [index, distance] of distances.entries()) {
    expect.soft(distance, probes[index][0]).toBeLessThanOrEqual(16);
  }
}

// Soft-checks the markup of an SVG export against `diagram` (from
// diagramOnCanvas): the file has no stylesheet, so the line carries its
// stroke and width inline. The editing affordances (hit area, focus band,
// connection handles) and the selection, by class or as a pressed toggle
// button, are left out. The copy of the canvas the image is drawn from is
// hidden and inert while it is in the page, but the file is neither, or a
// viewer opening it gets none of its text.
function expectDiagramInSvg(markup, diagram) {
  const root = markup.match(/<foreignObject[^>]*>\s*(<[^>]*>)/);
  expect.soft(root, 'the SVG has a root element').toBeTruthy();
  if (root) {
    expect
      .soft(root[1], 'SVG root not hidden from assistive technology')
      .not.toContain('aria-hidden');
  }
  expect
    .soft(markup, 'nothing in the SVG is inert')
    .not.toMatch(/\sinert[=\s>]/);
  const line = markup.match(
    /<path [^>]*class="builder-edge builder-edge--network-[^>]*>/,
  );
  expect.soft(line, 'the SVG has the connection line').toBeTruthy();
  if (line) {
    expect
      .soft(line[0], 'SVG line colour')
      .toContain(`stroke: ${diagram.stroke};`);
    expect
      .soft(line[0], 'SVG line width')
      .toContain(`stroke-width: ${diagram.width};`);
  }
  expect
    .soft(markup, 'no hit or focus paths')
    .not.toMatch(/builder-edge__(?:hit|focus)/);
  expect
    .soft(markup, 'no connection handles')
    .not.toContain('vue-flow__handle');
  expect.soft(markup, 'nothing selected').not.toContain('is-selected');
  expect.soft(markup, 'nothing pressed').not.toContain('aria-pressed');
  expect.soft(markup, 'no buttons').not.toContain('role="button"');
  expect.soft(markup, 'no Tab stops').not.toMatch(/tabindex=/i);
}

// Switches the page's colour scheme, which the builder's theme follows.
async function useColorScheme(page, scheme) {
  await page.emulateMedia({ colorScheme: scheme });
  await expect(page.locator('.builder-root')).toHaveAttribute(
    'data-builder-theme',
    scheme,
  );
}

async function download(page, trigger) {
  const [file] = await Promise.all([page.waitForEvent('download'), trigger()]);
  const path = await file.path();

  return {
    name: file.suggestedFilename(),
    path,
    buffer: fs.readFileSync(path),
  };
}

function exportFileName(title, extension) {
  const base = title
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return `${base || 'topology'}.${extension}`;
}

// The landing's Import button opens the Generate dialog, and its Upload
// button the Import dialog.
async function openGenerate(page) {
  await page.getByTestId('drafts-generate').click();
  const dialog = page.getByRole('dialog', {
    name: 'Import topology or experiment',
  });
  await expect(dialog).toBeVisible();

  return dialog;
}

async function openImport(page) {
  await page.getByTestId('drafts-import').click();
  const dialog = page.getByRole('dialog', { name: 'Upload diagram' });
  await expect(dialog).toBeVisible();

  return dialog;
}

// Uploads `content` through Generate > Uploaded config, submits it and
// returns the dialog and the POST /builder/generate response.
async function generateFromUpload(page, content, fileName = 'topology.yaml') {
  const dialog = await openGenerate(page);
  await dialog.getByLabel('Uploaded config').check();
  await dialog.getByTestId('generate-file').setInputFiles({
    name: fileName,
    mimeType: fileName.endsWith('.json')
      ? 'application/json'
      : 'application/yaml',
    buffer: Buffer.from(content),
  });
  await expect(dialog.getByTestId('generate-submit')).toBeEnabled();

  const generated = waitForApi(page, 'POST', '/builder/generate');
  await dialog.getByTestId('generate-submit').click();

  return { dialog, response: await generated };
}

// Generate stops on its warnings, and creates the draft only when the user
// continues past them. Continues when there are warnings; otherwise the
// dialog has closed by itself.
async function continuePastWarnings(dialog) {
  const next = dialog.getByTestId('generate-continue');
  await expect
    .poll(async () => (await next.isVisible()) || !(await dialog.isVisible()))
    .toBe(true);
  if (await next.isVisible()) {
    await next.click();
  }
}

function draftCard(page, id) {
  return page
    .getByTestId('drafts-list-mine')
    .locator('li', { has: page.getByTestId(`draft-open-${id}`) });
}

// The page-level error: an alert with its own Dismiss button.
function errorBanner(page) {
  return page.getByTestId('builder-error').getByRole('alert');
}

// --- export and import -------------------------------------------------------

test.describe('export and import', () => {
  test(
    'exports JSON, YAML, PNG and SVG, and re-imports the Builder files as new drafts',
    {
      tag: '@cross-browser',
    },
    async ({ page, builder, issues }, testInfo) => {
      await builder.open();
      const title = uniqueName(testInfo, 'export');
      const original = await buildConnectedDiagram(builder, title);
      const summary = await builder.summary.textContent();
      const saved = await builder.serverDocument(original);
      const labels = saved.nodes.map((node) => node.label);
      expect(labels).toHaveLength(2);

      // What the images must show: the diagram with nothing selected, in
      // each theme.
      const selected = page.locator('.builder-canvas .is-selected');
      await flowNode(builder, 'device').focus();
      await page.keyboard.press('Escape');
      await expect(selected).toHaveCount(0);
      const light = await diagramOnCanvas(page);
      expect(light.leftOfNodes, 'the line runs left of both nodes').toBe(true);
      await useColorScheme(page, 'dark');
      const dark = await diagramOnCanvas(page);
      await useColorScheme(page, 'light');
      expect.soft(dark.stroke, 'the dark line colour').not.toBe(light.stroke);

      // An image shows the diagram, not the editor's view of it: while the
      // images export, the canvas is zoomed in, which pans it too, and has
      // everything selected.
      await flowNode(builder, 'device').focus();
      await page.keyboard.press('ControlOrMeta+a');
      // Both nodes, the connection and its label.
      await expect(selected).toHaveCount(4);
      const zoomIn = page.getByRole('button', { name: 'Zoom in' });
      for (let step = 0; step < 3; step += 1) {
        await zoomIn.click();
      }
      await expect(
        page.locator('.vue-flow__transformationpane'),
      ).not.toHaveAttribute('style', /translate\(0px, 0px\) scale\(1\)/);

      const dialog = await builder.openDialog('export');
      const status = dialog.getByRole('status');
      const exportError = dialog.getByTestId('export-error');
      // The status region is there, empty, before the first export, so
      // screen readers announce the first message too.
      await expect.soft(status).toHaveText('');

      // The file name, the status line and the absence of an error, after
      // each download.
      async function expectSaved(file, extension) {
        const fileName = exportFileName(title, extension);
        expect.soft(file.name, `${extension} file name`).toBe(fileName);
        await expect.soft(status).toHaveText(`Saved ${fileName}.`);
        await expect
          .soft(exportError, `${extension} export error`)
          .toHaveCount(0);
      }

      const json =
        await test.step('JSON export is the saved document', async () => {
          const file = await download(page, () =>
            dialog.getByTestId('export-json').click(),
          );
          await expectSaved(file, 'json');

          const exported = JSON.parse(file.buffer.toString('utf8'));
          expect.soft(exported.$schema).toBe(SCHEMA_URI);
          expect.soft(exported.revision).toBe(1);
          expect.soft(exported.name).toBe(title);
          expect
            .soft(exported.nodes.map((node) => node.kind).sort())
            .toEqual(['device', 'switch']);
          expect
            .soft(exported.networks.map((network) => network.name))
            .toEqual(['EXP']);
          expect.soft(exported.edges).toHaveLength(1);
          expect.soft(exported.id).toBe(saved.id);
          expect.soft(exported.nodes).toEqual(saved.nodes);
          expect.soft(exported.edges).toEqual(saved.edges);

          return { file, exported };
        });

      const yaml =
        await test.step('YAML export has the document keys', async () => {
          const file = await download(page, () =>
            dialog.getByTestId('export-yaml').click(),
          );
          await expectSaved(file, 'yaml');

          // The e2e package has no YAML parser; check the top-level keys by line
          // and let the product's strict importer prove the document parses.
          const text = file.buffer.toString('utf8');
          expect
            .soft(text)
            .toMatch(
              /^\$schema: https:\/\/phenix\.sandia\.gov\/schemas\/builder\/v1$/m,
            );
          expect.soft(text).toMatch(/^revision: 1$/m);
          expect.soft(text).toContain(`name: ${title}\n`);
          expect.soft(text).toMatch(/^nodes:$/m);
          expect.soft(text.match(/^ {4}kind: device$/gm)).toHaveLength(1);
          expect.soft(text.match(/^ {4}kind: switch$/gm)).toHaveLength(1);

          return file;
        });

      const boundsText = dialog.getByText(/Diagram bounds: \d+ × \d+ px/);
      await expect(boundsText).toBeVisible();
      const [, boundsWidth, boundsHeight] = (await boundsText.textContent())
        .match(/(\d+) × (\d+)/)
        .map(Number);
      // An image's top-left corner: documentBounds() pads the nodes by
      // IMAGE_PADDING (40) on every side.
      const imageArea = {
        boundsWidth,
        origin: {
          x: Math.min(...saved.nodes.map((node) => node.position.x)) - 40,
          y: Math.min(...saved.nodes.map((node) => node.position.y)) - 40,
        },
      };

      await test.step('PNG export covers the whole diagram', async () => {
        // By keyboard: the button is busy while the image renders, and must
        // keep focus throughout rather than drop it outside the dialog.
        const button = dialog.getByTestId('export-png');
        const png = await download(page, () => button.press('Enter'));
        await expectSaved(png, 'png');
        await expect.soft(button).toBeFocused();
        // The export draws a copy of the canvas and removes it after; the
        // canvas keeps its selection.
        await expect
          .soft(page.locator('.vue-flow__transformationpane'))
          .toHaveCount(1);
        await expect.soft(selected).toHaveCount(4);
        expect.soft(png.buffer.length, 'PNG size').toBeGreaterThan(1000);
        expect
          .soft(png.buffer.subarray(0, 8), 'PNG signature')
          .toEqual(
            Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
          );
        // At least the diagram's size, with the diagram's aspect ratio (not the
        // on-screen viewport's). The IHDR chunk ends at byte 24.
        if (png.buffer.length >= 24) {
          const image = pngSize(png.buffer);
          expect.soft(image.width).toBeGreaterThanOrEqual(boundsWidth);
          expect
            .soft(image.width / image.height)
            .toBeCloseTo(boundsWidth / boundsHeight, 1);
        }
        await expectDiagramInImage(page, 'PNG', png.buffer, light, imageArea);
      });

      await test.step('SVG export draws every node label and the connection', async () => {
        const svg = await download(page, () =>
          dialog.getByTestId('export-svg').click(),
        );
        await expectSaved(svg, 'svg');
        const markup = svg.buffer.toString('utf8');
        const root = markup.match(/^<svg[^>]* width="(\d+)" height="(\d+)"/);
        expect(root, markup.slice(0, 200)).toBeTruthy();
        expect
          .soft(Number(root[1]) / Number(root[2]))
          .toBeCloseTo(boundsWidth / boundsHeight, 1);
        for (const label of labels) {
          expect.soft(markup).toContain(`>${label}<`);
        }
        expectDiagramInSvg(markup, light);
        await expectDiagramInImage(page, 'SVG', svg.buffer, light, imageArea);
      });

      await test.step('PNG and SVG exports draw the diagram in the dark theme', async () => {
        await useColorScheme(page, 'dark');
        const png = await download(page, () =>
          dialog.getByTestId('export-png').click(),
        );
        await expectSaved(png, 'png');
        await expectDiagramInImage(page, 'PNG', png.buffer, dark, imageArea);
        const svg = await download(page, () =>
          dialog.getByTestId('export-svg').click(),
        );
        await expectSaved(svg, 'svg');
        expectDiagramInSvg(svg.buffer.toString('utf8'), dark);
        await expectDiagramInImage(page, 'SVG', svg.buffer, dark, imageArea);
        await useColorScheme(page, 'light');
      });

      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(builder.dialog).toBeHidden();

      const jsonCopy =
        await test.step('the JSON file uploads from the editor toolbar as a new draft', async () => {
          // Named as on the drafts page: Import there converts a config.
          await expect
            .soft(builder.toolbar('import'))
            .toHaveAccessibleName('Upload');
          const importDialog = await builder.openDialog('import');
          await expect
            .soft(importDialog)
            .toHaveAccessibleName('Upload diagram');
          await expect
            .soft(importDialog.getByTestId('import-submit'))
            .toHaveText('Upload');
          await importDialog.getByTestId('import-file').setInputFiles({
            name: json.file.name,
            mimeType: 'application/json',
            buffer: json.file.buffer,
          });
          const created = waitForApi(page, 'POST', '/builder/drafts');
          await importDialog.getByTestId('import-submit').click();
          const imported = await (await created).json();

          await expect(builder.dialog).toBeHidden();
          expect.soft(imported.id, 'a new draft').not.toBe(original.id);
          await expect.soft(builder.summary).toHaveText(summary);
          await expect
            .soft(page.getByTestId('builder-name'))
            .toHaveValue(title);
          await builder.waitSaved();

          const copy = await builder.serverDocument(imported);
          expect.soft(copy.nodes).toEqual(json.exported.nodes);
          // The server drops empty optional fields, so compare the stored copies.
          expect.soft(copy.networks).toEqual(saved.networks);
          expect.soft(copy.edges).toEqual(json.exported.edges);
          expect
            .soft(await builder.serverDocument(original), 'the original draft')
            .toEqual(saved);

          return imported;
        });

      await builder.backToDrafts();
      await expect
        .soft(page.getByTestId(`draft-open-${original.id}`))
        .toBeVisible();
      await expect
        .soft(page.getByTestId(`draft-open-${jsonCopy.id}`))
        .toBeVisible();

      await test.step('the YAML file uploads from the drafts landing as a new draft', async () => {
        const importDialog = await openImport(page);
        await expect
          .soft(importDialog.getByLabel('File', { exact: true }))
          .toBeChecked();
        await importDialog.getByTestId('import-file').setInputFiles({
          name: yaml.name,
          mimeType: 'text/yaml',
          buffer: yaml.buffer,
        });
        const created = waitForApi(page, 'POST', '/builder/drafts');
        await importDialog.getByTestId('import-submit').click();
        const imported = await (await created).json();

        expect.soft(imported.id, 'a new draft').not.toBe(original.id);
        await expect(builder.canvas).toBeVisible();
        await expect.soft(builder.summary).toHaveText(summary);
        await expect.soft(page.getByTestId('builder-name')).toHaveValue(title);
        await builder.waitSaved();
        expect
          .soft((await builder.serverDocument(imported)).nodes)
          .toEqual(saved.nodes);
      });
      expectNoFatal(issues);
    },
  );

  test('Upload refuses input it cannot open, then uploads pasted text as a new draft', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    await builder.open();
    const creates = watchDraftCreates(page);

    let dialog = await openImport(page);
    const error = dialog.getByTestId('import-error');

    // Each refusal names the problem, marks the field and moves focus to it.
    await test.step('no file, then an oversized file', async () => {
      const file = dialog.getByTestId('import-file');
      const tooLarge = 'The uploaded file is larger than the 5 MiB limit.';

      await dialog.getByTestId('import-submit').click();
      await expect.soft(error).toHaveText('Choose a file to upload.');
      await expect.soft(file).toBeFocused();
      await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
      await expect
        .soft(file)
        .toHaveAccessibleDescription(
          'JSON or YAML, up to 5 MiB. Choose a file to upload.',
        );

      await file.setInputFiles({
        name: 'huge.json',
        mimeType: 'application/json',
        buffer: Buffer.alloc(5 * 1024 * 1024 + 1, 0x20),
      });
      await expect.soft(error).toHaveText(tooLarge);

      // Submitting the oversized file keeps the size error. The submit
      // handler runs in the click task; a later task sees its DOM update.
      await dialog.getByTestId('import-submit').click();
      await page.evaluate(() => null);
      expect.soft(await error.textContent()).toBe(tooLarge);
      await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
      expect(creates).toEqual([]);
    });

    // The Topology and Experiment refusals name the kind and point at
    // Import on the drafts page. Their article is checked by the "an before
    // Experiment" test.
    const name = uniqueName(testInfo, 'paste');
    const refusals = [
      ['empty text', '   ', 'Nothing to upload: the document is empty.'],
      [
        'an unknown field',
        JSON.stringify({ ...blankDocument('strict'), extra: true }),
        'document: unknown field "extra"',
      ],
      [
        'another schema',
        JSON.stringify({
          ...blankDocument('strict'),
          $schema: 'https://example.com/other',
        }),
        'Unsupported builder document schema "https://example.com/other" ' +
          `(expected "${SCHEMA_URI}").`,
      ],
      // The parser's code frame is dropped: read aloud it is punctuation.
      [
        'unparsable YAML',
        'nodes: [unclosed',
        /^Could not parse the document: .+ \(line \d+, column \d+\)$/,
      ],
      [
        'a Topology config',
        [
          `apiVersion: ${API_VERSION}`,
          'kind: Topology',
          'metadata:',
          `  name: ${name}`,
          'spec:',
          '  nodes: []',
          '',
        ].join('\n'),
        /Topology config is not a Builder document\. Use Import on the drafts page\b/,
      ],
      [
        'an Experiment config',
        JSON.stringify({
          apiVersion: API_VERSION,
          kind: 'Experiment',
          metadata: { name },
          spec: {},
        }),
        /Experiment config is not a Builder document\. Use Import on the drafts page\b/,
      ],
    ];

    await dialog.getByLabel('Paste text', { exact: true }).check();
    for (const [what, text, message] of refusals) {
      await test.step(`pasted ${what}`, async () => {
        const field = dialog.getByTestId('import-text');
        await field.fill(text);
        await dialog.getByTestId('import-submit').click();
        await expect.soft(error, what).toHaveText(message);
        await expect.soft(field, what).toHaveAttribute('aria-invalid', 'true');
        await expect.soft(field, what).toBeFocused();
        // An accepted input closes the dialog and creates a draft; stop here
        // rather than wait out the next case's fill.
        await expect(dialog, what).toBeVisible();
        expect(creates, what).toEqual([]);
      });
    }

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
    await expect
      .soft(page.getByRole('heading', { name: 'Builder Flow' }))
      .toBeVisible();
    expect.soft(creates, 'draft creates after Cancel').toEqual([]);

    await test.step('a pasted builder document opens as a new draft', async () => {
      const title = uniqueName(testInfo, 'paste-ok');
      const document = blankDocument(title);

      dialog = await openImport(page);
      await dialog.getByLabel('Paste text', { exact: true }).check();
      await dialog.getByTestId('import-text').fill(JSON.stringify(document));

      const created = waitForApi(page, 'POST', '/builder/drafts');
      await dialog.getByTestId('import-submit').click();
      const draft = await (await created).json();

      await expect(dialog).toBeHidden();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(page.getByTestId('builder-name')).toHaveValue(title);
      await expect
        .soft(builder.summary)
        .toContainText('0 devices, 0 switches, 0 networks');
      await builder.waitSaved();

      expect.soft((await builder.serverDraft(draft)).title).toBe(title);
      const doc = await builder.serverDocument(draft);
      expect
        .soft(doc)
        .toMatchObject({ $schema: SCHEMA_URI, id: document.id, name: title });
    });
    expectNoFatal(issues);
  });

  test('published diagrams open as new drafts from a deep link and from Upload', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'published');
    const deepLink = `/builder-beta?topology=${encodeURIComponent(name)}`;
    const creates = watchDraftCreates(page);

    await test.step('a deep link to an unpublished topology shows an error', async () => {
      await visit(page, deepLink);
      await expect(
        page.getByRole('heading', { name: 'Builder Flow' }),
      ).toBeVisible({ timeout: 20000 });
      await expect
        .soft(errorBanner(page))
        .toHaveText(
          `No published Builder Flow document exists for topology ${name}. Use Import to make a diagram from it.`,
        );
      await expect.soft(builder.canvas).toHaveCount(0);
      await expect.soft(page.getByTestId('drafts-blank')).toBeEnabled();
      expect.soft(creates, 'draft creates').toEqual([]);
    });

    // Each path opens a document that has not been opened before: a second
    // copy of an opened document is a separate defect (see the ?topology=
    // link test in builder-persistence.spec.js), so its fix may not create
    // a new draft.
    const linked = await publishDiagram(request, tracker, name);
    const imported = await publishDiagram(
      request,
      tracker,
      uniqueName(testInfo, 'published-import'),
    );

    async function expectOpened(draft, published) {
      expect.soft(draft.id, 'a new draft').not.toBe(published.draft.id);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await expect
        .soft(page.getByTestId('builder-name'))
        .toHaveValue(published.name);
      await expect
        .soft(builder.summary)
        .toContainText('1 device, 1 switch, 1 network, 1 connection');
      await expect.soft(builder.outlineItem('pub-host')).toBeVisible();
      await builder.waitSaved();
      expect
        .soft((await builder.serverDraft(draft)).sourceToken)
        .toBe(`builder-doc/${published.documentId}`);
    }

    await test.step('the deep link opens the published diagram', async () => {
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await visit(page, deepLink);
      await expectOpened(await (await created).json(), linked);
    });

    await builder.backToDrafts();

    await test.step('Upload > Published diagram requires a selection, then opens it', async () => {
      const dialog = await openImport(page);
      await dialog.getByLabel('Published diagram', { exact: true }).check();
      const choice = dialog.getByTestId('import-published');
      await expect.soft(choice).toHaveValue('');
      // A published diagram is on the server already: it is opened.
      await expect
        .soft(dialog.getByTestId('import-submit'))
        .toHaveAccessibleName('Open');
      const opened = creates.length;
      await dialog.getByTestId('import-submit').click();
      await expect
        .soft(dialog.getByTestId('import-error'))
        .toHaveText('Select a published diagram.');
      await expect(dialog).toBeVisible();
      expect.soft(creates, 'draft creates').toHaveLength(opened);

      await choice.selectOption({ label: imported.name });
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await dialog.getByTestId('import-submit').click();
      await expectOpened(await (await created).json(), imported);
    });
    expectNoFatal(issues);
  });

  test('rejects a YAML import whose aliases expand past the 5 MiB limit', async ({
    page,
    builder,
  }) => {
    await builder.open();

    // Under 2 KB of YAML that would expand to about 11 MB (more than twice
    // the 5 MiB import limit): four levels of ten aliases over a
    // 1000-character string. The import refuses aliases before they expand
    //. Kept this small so a regression cannot hang the runner: it
    // would close the dialog and fail later with a 413 banner.
    const leaf = 'x'.repeat(1000);
    const ten = (ref) => `[${Array(10).fill(`*${ref}`).join(', ')}]`;
    const id = crypto.randomUUID();
    const bomb = [
      `$schema: ${SCHEMA_URI}`,
      'revision: 1',
      `id: ${id}`,
      'name: alias-bomb',
      'viewport: {x: 0, y: 0, zoom: 1}',
      'grid: {enabled: true, size: 16, snap: true}',
      'networks: []',
      'edges: []',
      'nodes:',
      `  - id: ${crypto.randomUUID()}`,
      '    kind: device',
      '    label: bomb',
      '    position: {x: 0, y: 0}',
      '    device:',
      '      hostname: bomb',
      '      iconKey: server',
      '      interfaces: []',
      '      spec:',
      '        type: VirtualMachine',
      '        general: {hostname: bomb}',
      '        advanced:',
      `          a: &a "${leaf}"`,
      `          b: &b ${ten('a')}`,
      `          c: &c ${ten('b')}`,
      `          d: &d ${ten('c')}`,
      `          e: ${ten('d')}`,
      '',
    ].join('\n');
    expect(bomb.length).toBeLessThan(2048);

    const creates = watchDraftCreates(page);
    const dialog = await openImport(page);
    await dialog.getByLabel('Paste text', { exact: true }).check();
    await dialog.getByTestId('import-text').fill(bomb);
    await dialog.getByTestId('import-submit').click();

    // Wait for either outcome, so a regression (the expanded document is
    // sent as a new draft) is reported at once.
    const error = dialog.getByTestId('import-error');
    await expect
      .poll(async () => creates.length > 0 || (await error.isVisible()), {
        message: 'the import is refused or a draft is created',
      })
      .toBe(true);
    expect(creates, 'draft creates for the expanded document').toEqual([]);
    await expect(error).toContainText(
      'YAML aliases (*name) are not supported.',
    );
  });

  test('Import and Generate copy use "an" before Experiment', async ({
    page,
    builder,
  }) => {
    await builder.open();

    const importDialog = await openImport(page);
    await importDialog.getByLabel('Paste text', { exact: true }).check();
    await importDialog.getByTestId('import-text').fill(
      JSON.stringify({
        apiVersion: API_VERSION,
        kind: 'Experiment',
        metadata: { name: 'grammar' },
        spec: {},
      }),
    );
    await importDialog.getByTestId('import-submit').click();
    await expect(importDialog.getByTestId('import-error')).toHaveText(
      /^An Experiment config is not a Builder document\./,
    );
    await importDialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(importDialog).toBeHidden();

    const generateDialog = await openGenerate(page);
    await generateDialog
      .getByTestId('generate-kind')
      .selectOption('experiment');
    const placeholder = generateDialog
      .getByTestId('generate-name')
      .locator('option')
      .first();
    await expect(placeholder).toHaveText('Choose an experiment');
  });
});

// --- generate ----------------------------------------------------------------

test.describe('generate', () => {
  test('generates a draft from a stored topology; a kind with no configs shows an empty state', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'gen-topo');
    // The topology includes `child`, which includes `nested`: their nodes are
    // added to the diagram read only, and svc-host brings a VLAN of its own.
    const child = uniqueName(testInfo, 'gen-topo-inc');
    const nested = uniqueName(testInfo, 'gen-topo-nest');
    await seedConfig(
      request,
      tracker,
      topologyConfig(nested, [topologyNode('deep-host', [['eth0', 'MGMT']])]),
    );
    const childConfig = topologyConfig(child, [
      topologyNode('inc-host', [['eth0', 'EXP']]),
      topologyNode('svc-host', [['eth0', 'SERVICES']]),
    ]);
    childConfig.spec.includeTopologies = [nested];
    await seedConfig(request, tracker, childConfig);
    // The root's hosts are a Firewall and a Router, node types that the
    // import and the publication keep.
    const rootNodes = sharedVlanNodes();
    rootNodes[0].type = 'Firewall';
    rootNodes[1].type = 'Router';
    const rootConfig = topologyConfig(name, rootNodes);
    rootConfig.spec.includeTopologies = [child];
    await seedConfig(request, tracker, rootConfig);

    // Other specs may leave experiments on a shared server; answer the
    // sources request with the real catalog minus its experiments so the
    // empty state is deterministic.
    await page.route(`**${API}/builder/sources`, async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      await route.fulfill({ response, json: { ...body, experiments: [] } });
    });
    await builder.open();
    // Stored after the landing page read the list: Generate reads it again
    // when it opens.
    const gone = uniqueName(testInfo, 'gen-gone');
    await seedConfig(request, tracker, topologyConfig(gone, []));

    const dialog = await openGenerate(page);
    const kind = dialog.getByTestId('generate-kind');
    const choices = dialog.getByTestId('generate-name');
    const empty = dialog.getByTestId('generate-empty');
    const submit = dialog.getByTestId('generate-submit');
    await expect.soft(dialog.getByLabel('Stored config')).toBeChecked();
    await expect.soft(kind).toHaveValue('topology');
    await expect.soft(empty).toHaveCount(0);
    await expect
      .soft(choices.locator('option', { hasText: name }))
      .toHaveCount(1);
    await expect.soft(submit).toBeDisabled();

    await test.step('a topology removed after the list was read is reported on its select', async () => {
      await expect(choices.locator('option', { hasText: gone })).toHaveCount(1);
      await choices.selectOption(gone);
      const removed = await request.delete(`${API}/configs/Topology/${gone}`);
      expect(removed.ok(), await removed.text()).toBeTruthy();

      await submit.click();
      await expect
        .soft(dialog.getByTestId('generate-error'))
        .toHaveText(
          `Could not import the diagram. The topology "${gone}" no longer exists. Choose another one.`,
        );
      await expect.soft(choices).toHaveAttribute('aria-invalid', 'true');
      await expect
        .soft(choices)
        .toHaveAccessibleDescription(
          /no longer exists\. Choose another one\.$/,
        );
      await expect.soft(choices).toBeFocused();
      // The list is read again, so the removed topology is no longer offered.
      await expect
        .soft(choices.locator('option', { hasText: gone }))
        .toHaveCount(0);
      await expect.soft(choices).toHaveValue('');
    });

    await test.step('a kind with no stored configs shows an empty state', async () => {
      await kind.selectOption('experiment');
      await expect
        .soft(empty)
        .toHaveText(
          'This phenix instance has no experiment configs to import.',
        );
      // Only the placeholder remains (its wording is covered by the grammar
      // test), and nothing can be generated.
      await expect.soft(choices.locator('option')).toHaveCount(1);
      await expect.soft(choices).toHaveValue('');
      await expect.soft(submit).toBeDisabled();
    });

    await kind.selectOption('topology');
    await expect.soft(empty).toHaveCount(0);
    await expect(choices.locator('option', { hasText: name })).toHaveCount(1);
    await choices.selectOption(name);

    const generated = waitForApi(page, 'POST', '/builder/generate');
    const created = waitForApi(page, 'POST', '/builder/drafts');
    await submit.click();
    const result = await (await generated).json();

    // The nested include is followed, and the warning says what was added.
    const added =
      `Added 3 nodes from included topologies ${child} (2 nodes) and ` +
      `${nested} (1 node). They are shown read only`;
    expect.soft(result.warnings, 'generation warnings').toHaveLength(1);
    expect.soft(result.warnings[0]).toContain(added);
    await expect
      .soft(dialog.getByTestId('generate-warnings'))
      .toContainText(added);
    await continuePastWarnings(dialog);
    const draft = await (await created).json();

    await expect(dialog).toBeHidden();
    await expect.soft(page.getByTestId('builder-name')).toHaveValue(name);
    await expect
      .soft(builder.summary)
      .toContainText(
        '5 devices, 3 switches, 3 networks, 6 connections, 0 groups, 0 notes',
      );
    await expect.soft(builder.nodes('device')).toHaveCount(5);
    await expect.soft(builder.nodes('switch')).toHaveCount(3);
    await expect
      .soft(builder.outlineItem('host-a'))
      .toHaveAttribute('aria-label', /1 connection, on EXP$/);
    await expect
      .soft(builder.outlineItem('host-b'))
      .toHaveAttribute('aria-label', /2 connections, on EXP and MGMT$/);
    for (const [hostname, from] of [
      ['inc-host', child],
      ['svc-host', child],
      ['deep-host', nested],
    ]) {
      const row = builder.outlineItem(hostname);
      await expect.soft(row).toContainText('included');
      await expect
        .soft(row)
        .toHaveAttribute(
          'aria-label',
          new RegExp(`, from included topology ${from}, read only, `),
        );
    }
    const networks = page.getByTestId('builder-networks');
    await expect.soft(networks).toContainText('EXP');
    await expect.soft(networks).toContainText('MGMT');
    await expect.soft(networks).toContainText('SERVICES');
    await builder.waitSaved();

    const stored = await builder.serverDraft(draft);
    expect.soft(stored.sourceToken).toBe(`Topology/${name}`);
    const doc = await builder.serverDocument(draft);
    expect
      .soft(doc.source)
      .toMatchObject({ kind: 'topology', name, includeTopologies: [child] });
    expect
      .soft(doc.networks.map((network) => network.name).sort())
      .toEqual(['EXP', 'MGMT', 'SERVICES']);
    const types = Object.fromEntries(
      doc.nodes
        .filter((node) => node.kind === 'device')
        .map((node) => [node.device.hostname, node.device.spec.type]),
    );
    expect
      .soft(types)
      .toMatchObject({ 'host-a': 'Firewall', 'host-b': 'Router' });

    await test.step('publishing stores the root nodes and the include only', async () => {
      const publish = await builder.openDialog('publish');
      // SERVICES and its switch are there only for svc-host.
      await expect
        .soft(publish)
        .toContainText(
          '2 devices, 2 switches, 2 networks and 3 connections are ready to publish. ' +
            'The 3 devices from included topologies and their 3 connections, and the ' +
            '1 switch and 1 network only they use, are not copied',
        );
      await expect(publish.getByTestId('publish-name')).toHaveValue(name);
      await expect(publish.getByTestId('publish-submit')).toHaveText(
        'Update topology',
      );

      const published = page.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname.endsWith('/publish'),
      );
      await publish.getByTestId('publish-submit').click();
      // Publish asks before it replaces the topology.
      await page.getByTestId('confirm-accept').click();
      const response = await published;
      expect(response.status(), await response.text()).toBe(200);
      await expect
        .soft(publish.getByTestId('publish-summary'))
        .toHaveText('Published. Every stage succeeded.');

      const topology = await builder.config('Topology', name);
      expect
        .soft(topology.spec.nodes.map((node) => node.general.hostname))
        .toEqual(['host-a', 'host-b']);
      expect
        .soft(topology.spec.nodes.map((node) => node.type))
        .toEqual(['Firewall', 'Router']);
      expect.soft(topology.spec.includeTopologies).toEqual([child]);
    });
    expectNoFatal(issues);
  });

  test('generates a draft from a stored experiment with VLAN aliases and included topologies', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const topology = uniqueName(testInfo, 'gen-exp-topo');
    const experiment = uniqueName(testInfo, 'gen-exp');
    // The topology includes `child`, which includes `nested`: phenix merges
    // both into the experiment when it creates it.
    const child = uniqueName(testInfo, 'gen-exp-inc');
    const nested = uniqueName(testInfo, 'gen-exp-nest');
    await seedConfig(
      request,
      tracker,
      topologyConfig(nested, [topologyNode('deep-host', [['eth0', 'MGMT']])]),
    );
    const childConfig = topologyConfig(child, [
      topologyNode('inc-host', [['eth0', 'EXP']]),
    ]);
    childConfig.spec.includeTopologies = [nested];
    await seedConfig(request, tracker, childConfig);
    const rootConfig = topologyConfig(topology, sharedVlanNodes());
    rootConfig.spec.includeTopologies = [child];
    await seedConfig(request, tracker, rootConfig);
    await createExperiment(request, tracker, experiment, topology);
    await setExperimentAliases(request, experiment, { EXP: 101, MGMT: 102 });
    await builder.open();

    const dialog = await openGenerate(page);
    await dialog.getByTestId('generate-kind').selectOption('experiment');
    await dialog.getByTestId('generate-name').selectOption(experiment);

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await dialog.getByTestId('generate-submit').click();
    // The nodes phenix merged in from the included topologies are marked,
    // not added again. Experiment fields the diagram does not carry come
    // back as a warning too.
    await expect
      .soft(dialog.getByTestId('generate-warnings'))
      .toContainText(
        `Marked 2 experiment nodes as coming from included topologies ${child} (1 node) and ${nested} (1 node).`,
      );
    await continuePastWarnings(dialog);
    const draft = await (await created).json();

    await expect(page.getByTestId('builder-name')).toHaveValue(experiment);
    await builder.expectSummary(
      '4 devices, 2 switches, 2 networks, 5 connections',
    );
    await expect(
      page.locator('[data-testid^="outline-item-"][aria-label^="Switch EXP,"]'),
    ).toHaveAttribute('aria-label', /VLAN alias 101/);
    await expect(
      page.locator(
        '[data-testid^="outline-item-"][aria-label^="Switch MGMT,"]',
      ),
    ).toHaveAttribute('aria-label', /VLAN alias 102/);
    const networks = page.getByTestId('builder-networks');
    await expect(networks).toContainText('VLAN 101');
    await expect(networks).toContainText('VLAN 102');
    await builder.waitSaved();

    const stored = await builder.serverDraft(draft);
    expect(stored.sourceToken).toBe(`Experiment/${experiment}`);
    const doc = await builder.serverDocument(draft);
    expect(doc.source).toMatchObject({ kind: 'experiment', name: experiment });
    const aliases = Object.fromEntries(
      doc.networks.map((network) => [network.name, network.alias]),
    );
    expect(aliases).toEqual({ EXP: 101, MGMT: 102 });
    // Publishing writes the include, never the included devices again.
    expect.soft(doc.source.includeTopologies).toEqual([child]);
    expect
      .soft(
        Object.fromEntries(
          doc.nodes
            .filter((node) => node.kind === 'device')
            .map((node) => [
              node.device.hostname,
              node.device.includedFrom || '',
            ]),
        ),
      )
      .toEqual({
        'host-a': '',
        'host-b': '',
        'inc-host': child,
        'deep-host': nested,
      });

    await test.step('devices of included topologies are shown read only', async () => {
      const row = builder.outlineItem('inc-host');
      await expect.soft(row).toContainText('included');
      await expect
        .soft(row)
        .toHaveAttribute(
          'aria-label',
          new RegExp(`, from included topology ${child}, read only, `),
        );
      await expect
        .soft(builder.node('deep-host', 'device'))
        .toContainText(`Included from ${nested}`);

      // Delete is refused with the reason, and the row keeps focus.
      await row.focus();
      await row.press('Delete');
      await expect
        .soft(page.getByTestId('canvas-notice'))
        .toContainText(
          `inc-host comes from included topology ${child}, so it is read only here.`,
        );
      await expect.soft(row).toBeFocused();
      await builder.expectSummary('4 devices');

      // The Inspector says where it is edited, and offers no edits.
      await builder.selectInOutline('inc-host');
      await expect
        .soft(builder.inspector.getByTestId('inspector-included-note'))
        .toContainText(`Change it in ${child}`);
      await expect
        .soft(builder.inspector.getByTestId('inspector-add-interface'))
        .toHaveCount(0);
    });
    expectNoFatal(issues);
  });

  test('devices generated from an experiment keep their VM icon', async ({
    page,
    request,
    builder,
    tracker,
  }, testInfo) => {
    const topology = uniqueName(testInfo, 'icon-topo');
    const experiment = uniqueName(testInfo, 'icon-exp');
    await seedConfig(
      request,
      tracker,
      topologyConfig(topology, sharedVlanNodes()),
    );
    await createExperiment(request, tracker, experiment, topology);

    // The same VMs generated from the topology give the expected icons.
    // Generating stores nothing, so this call needs no cleanup.
    const iconsOf = (doc) =>
      doc.nodes
        .filter((node) => node.kind === 'device')
        .map((node) => node.device.iconKey)
        .sort();
    const fromTopology = await request.post(`${API}/builder/generate`, {
      data: { source: `topology/${topology}` },
    });
    expect(fromTopology.ok(), await fromTopology.text()).toBeTruthy();
    const expected = iconsOf((await fromTopology.json()).document);
    expect(expected).toHaveLength(2);
    expect(expected).not.toContain('external');

    await builder.open();
    const dialog = await openGenerate(page);
    await dialog.getByTestId('generate-kind').selectOption('experiment');
    await dialog.getByTestId('generate-name').selectOption(experiment);
    const created = waitForApi(page, 'POST', '/builder/drafts');
    await dialog.getByTestId('generate-submit').click();
    await continuePastWarnings(dialog);
    const draft = await (await created).json();

    // The stored icons first: phenix stores "external": null on every VM
    // of an experiment, which marked them all external.
    expect(iconsOf(await builder.serverDocument(draft))).toEqual(expected);
    const devices = builder.nodes('device');
    await expect(devices).toHaveCount(2);
    // The canvas must not draw these VMs as external (hardware) devices.
    await expect(devices.locator('.builder-icon--external')).toHaveCount(0);
  });

  test(
    'Generate refuses an uploaded config it cannot convert and converts an uploaded YAML topology',
    {
      tag: '@cross-browser',
    },
    async ({ page, builder, issues }, testInfo) => {
      await builder.open();
      const creates = watchDraftCreates(page);

      await test.step('an uploaded Scenario is refused', async () => {
        const scenario = [
          `apiVersion: phenix.sandia.gov/v2`,
          'kind: Scenario',
          'metadata:',
          `  name: ${uniqueName(testInfo, 'scenario')}`,
          'spec:',
          '  apps: []',
          '',
        ].join('\n');

        const { dialog, response } = await generateFromUpload(page, scenario);
        expect.soft(response.status(), 'generate status').toBe(422);
        await expect
          .soft(dialog.getByTestId('generate-error'))
          .toHaveText(
            'Could not import the diagram. ' +
              'Scenario configs cannot be opened in the builder.',
          );
        await expect(dialog).toBeVisible();
        // The refusal is about the uploaded file, so it is reported there.
        const file = dialog.getByTestId('generate-file');
        await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
        await expect.soft(file).toBeFocused();
        await expect
          .soft(file)
          .toHaveAccessibleDescription(/cannot be opened in the builder\.$/);
        await expect.soft(dialog.getByTestId('generate-submit')).toBeEnabled();
        expect.soft(creates, 'draft creates').toEqual([]);

        await dialog.getByRole('button', { name: 'Cancel' }).click();
        await expect(dialog).toBeHidden();
      });

      const name = uniqueName(testInfo, 'upload');
      const content = [
        `apiVersion: ${API_VERSION}`,
        'kind: Topology',
        'metadata:',
        `  name: ${name}`,
        'spec:',
        '  nodes:',
        '  - type: VirtualMachine',
        '    general: {hostname: web, vm_type: kvm}',
        '    hardware:',
        '      os_type: linux',
        '      drives: [{image: miniccc.qc2}]',
        '    network:',
        '      interfaces:',
        '      - {name: eth0, vlan: EXP, address: 10.0.0.1, mask: 24, proto: static, type: ethernet}',
        '  - type: VirtualMachine',
        '    general: {hostname: db, vm_type: kvm}',
        '    hardware:',
        '      os_type: linux',
        '      drives: [{image: miniccc.qc2}]',
        '    network:',
        '      interfaces:',
        '      - {name: eth0, vlan: EXP, address: 10.0.0.2, mask: 24, proto: static, type: ethernet}',
        '',
      ].join('\n');

      const created = waitForApi(page, 'POST', '/builder/drafts');
      const { response } = await generateFromUpload(page, content);
      expect(response.ok(), await response.text()).toBeTruthy();
      const draft = await (await created).json();

      await expect.soft(page.getByTestId('builder-name')).toHaveValue(name);
      await expect
        .soft(builder.summary)
        .toContainText('2 devices, 1 switch, 1 network, 2 connections');
      await expect.soft(builder.outlineItem('web')).toBeVisible();
      await expect.soft(builder.outlineItem('db')).toBeVisible();
      await builder.waitSaved();

      const stored = await builder.serverDraft(draft);
      expect.soft(stored.sourceToken).toBe(`uploaded/Topology/${name}`);
      const doc = await builder.serverDocument(draft);
      expect.soft(doc.source).toMatchObject({ kind: 'topology', name });
      expect
        .soft(
          doc.nodes
            .filter((node) => node.kind === 'device')
            .map((node) => node.device.hostname)
            .sort(),
        )
        .toEqual(['db', 'web']);
      expect.soft(doc.networks.map((network) => network.name)).toEqual(['EXP']);
      expect.soft(doc.edges).toHaveLength(2);
      expectNoFatal(issues);
    },
  );

  test('shows generation warnings before opening the draft', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    await builder.open();
    const name = uniqueName(testInfo, 'warn');
    const content = JSON.stringify(
      topologyConfig(name, [
        topologyNode('dup', [['eth0', 'EXP']]),
        topologyNode('dup', [['eth0', 'EXP']]),
      ]),
    );

    const creates = watchDraftCreates(page);
    await recordAnnouncements(page);

    // Cancel, left of Continue, closes the dialog as Escape does: no draft,
    // no word of an import, and focus back on Import.
    await test.step('Cancel leaves the warnings without creating a draft', async () => {
      const { dialog } = await generateFromUpload(
        page,
        content,
        'topology.json',
      );
      await expect(dialog.getByTestId('generate-warnings')).toBeVisible();
      await expect
        .soft(dialog.locator('.builder-dialog__actions button'))
        .toHaveText(['Cancel', 'Continue to editor']);
      await dialog.getByTestId('generate-cancel').click();
      await expect(dialog).toBeHidden();
      await expect.soft(page.getByTestId('drafts-generate')).toBeFocused();
      await expect.soft(builder.landingHeading).toBeVisible();
      expect.soft(creates, 'draft creates after Cancel').toEqual([]);
      // Messages held while the dialog was open show once it closes, so
      // the recording is checked again once the next import is announced.
      await expect.soft(builder.liveRegion).not.toContainText(/imported/i);
    });

    const created = waitForApi(page, 'POST', '/builder/drafts');
    const { dialog, response } = await generateFromUpload(
      page,
      content,
      'topology.json',
    );
    const { warnings } = await response.json();
    expect(warnings).toEqual([
      expect.stringContaining('duplicates the hostname'),
    ]);

    // The dialog stays open on the warnings, with focus on the way on, whose
    // description is the warnings themselves. No draft exists yet.
    const shown = dialog.getByTestId('generate-warnings');
    const next = dialog.getByRole('button', { name: 'Continue to editor' });
    await expect(shown).toContainText('duplicates the hostname');
    await expect.soft(next).toBeFocused();
    await expect
      .soft(next)
      .toHaveAccessibleDescription(/duplicates the hostname/);
    await expect
      .soft(dialog.getByRole('status'))
      .toHaveText('This import has 1 warning.');
    expect.soft(creates, 'draft creates before Continue').toEqual([]);

    await next.press('Enter');
    await expect(dialog).toBeHidden();
    // The import is announced once its draft exists, and only this one:
    // nothing said the cancelled import was imported.
    await expect
      .soft(builder.liveRegion)
      .toContainText('The diagram was imported with 1 warning. Draft created.');
    expect
      .soft(
        (await recordedAnnouncements(page)).filter((text) =>
          /imported/i.test(text),
        ),
        'announcements of an import',
      )
      .toEqual([
        expect.stringContaining(
          'The diagram was imported with 1 warning. Draft created.',
        ),
      ]);
    await expect(builder.canvas).toBeVisible();
    await expect.soft(builder.outlineItem('dup')).toBeVisible();
    await (await created).json();
    // Focus follows into the editor rather than falling to the page.
    expect
      .soft(await page.evaluate(() => document.activeElement !== document.body))
      .toBe(true);
    await builder.waitSaved();
    expectNoFatal(issues);
  });

  // minimega VLAN names are case sensitive, so EXP and exp are two isolated
  // segments, which one network would merge on publish.
  test('keeps VLANs that differ only by case as separate networks', async ({
    page,
    builder,
  }, testInfo) => {
    await builder.open();
    const name = uniqueName(testInfo, 'case');
    const content = JSON.stringify(
      topologyConfig(name, [
        topologyNode('upper', [['eth0', 'EXP']]),
        topologyNode('lower', [['eth0', 'exp']]),
      ]),
    );

    const { dialog, response } = await generateFromUpload(
      page,
      content,
      'topology.json',
    );
    const { document } = await response.json();
    expect(document.networks, 'generated networks').toHaveLength(2);
    // The difference may be a typo, so the import says so.
    await expect
      .soft(dialog.getByTestId('generate-warnings'))
      .toContainText(
        'VLAN "exp" differs only by case from VLAN "EXP"; minimega treats them as different VLANs, so they are separate networks',
      );
    await continuePastWarnings(dialog);
    await expect(page.getByTestId('builder-name')).toHaveValue(name);
    await builder.expectSummary(
      '2 devices, 2 switches, 2 networks, 2 connections',
    );
  });

  test(
    'uploaded configs are converted verbatim, without environment expansion',
    {
      tag: '@known-defect',
    },
    async ({ page, builder }, testInfo) => {
      // Uploads are limited to users who may create configs, who can reach
      // the same substitution through POST /configs; removing it is left to
      // the maintainers (see sandialabs/sceptre-phenix#436).
      knownDefect(
        'generate expands ${VAR} from the server environment for users who may create configs (sandialabs/sceptre-phenix#436)',
      );
      await builder.open();
      const name = uniqueName(testInfo, 'env');
      // phenix's ${NAME:default} syntax makes the expansion observable without
      // depending on (or revealing) any real server environment variable.
      const placeholder = '${PHENIX_E2E_UNSET_VARIABLE:expanded}';
      const content = JSON.stringify(
        topologyConfig(name, [
          topologyNode('envhost', [['eth0', 'EXP']], {
            description: placeholder,
          }),
        ]),
      );
      const descriptionOf = (doc) =>
        doc.nodes.find((node) => node.kind === 'device').device.spec.general
          .description;

      const created = waitForApi(page, 'POST', '/builder/drafts');
      const { response } = await generateFromUpload(
        page,
        content,
        'topology.json',
      );
      const draft = await (await created).json();
      // The generated document first, so the expected failure is immediate.
      expect(descriptionOf((await response.json()).document)).toBe(placeholder);
      await builder.waitSaved();
      expect(descriptionOf(await builder.serverDocument(draft))).toBe(
        placeholder,
      );
    },
  );
});

// --- drafts landing ----------------------------------------------------------

test.describe('drafts landing', () => {
  test(
    'collection tabs move with the arrow keys',
    {
      tag: '@cross-browser',
    },
    async ({ page, builder }) => {
      await builder.open();

      const tabs = ['mine', 'shared', 'published'].map((id) =>
        page.getByTestId(`drafts-tab-${id}`),
      );
      const panels = ['mine', 'shared', 'published'].map((id) =>
        page.locator(`#panel-${id}`),
      );

      async function expectActive(index) {
        for (const [i, tab] of tabs.entries()) {
          await expect(tab).toHaveAttribute(
            'aria-selected',
            String(i === index),
          );
          await expect(tab).toHaveAttribute(
            'tabindex',
            i === index ? '0' : '-1',
          );
          await expect(panels[i]).toBeVisible({ visible: i === index });
        }
        await expect(tabs[index]).toBeFocused();
      }

      await expect(
        page.getByRole('tablist', { name: 'Builder collections' }),
      ).toBeVisible();
      await tabs[0].focus();
      await expect(tabs[0]).toHaveAttribute('aria-selected', 'true');

      await page.keyboard.press('ArrowRight');
      await expectActive(1);
      await page.keyboard.press('ArrowRight');
      await expectActive(2);
      await page.keyboard.press('ArrowRight');
      await expectActive(0);
      await page.keyboard.press('ArrowLeft');
      await expectActive(2);
      await page.keyboard.press('ArrowLeft');
      await expectActive(1);

      await tabs[0].click();
      await expectActive(0);
    },
  );

  // There is no Refresh button: the landing reads its lists again when the
  // page comes back into view, quietly and once for the pair of events.
  test('the lists refresh when the page comes back into view, and Delete removes a draft', async ({
    page,
    request,
    builder,
    issues,
  }, testInfo) => {
    const initial = waitForApi(page, 'GET', '/builder/drafts');
    await builder.open();
    await initial;
    await expect(page.getByTestId('drafts-refresh')).toHaveCount(0);

    const title = uniqueName(testInfo, 'refresh');
    const draft = await builder.seedDraft(blankDocument(title));
    const path = draftPath(draft);
    const open = page.getByTestId(`draft-open-${draft.id}`);
    await expect
      .soft(open, 'listed before the page is shown again')
      .toHaveCount(0);

    const listings = [];
    page.on('request', (sent) => {
      if (
        sent.method() === 'GET' &&
        new URL(sent.url()).pathname.endsWith(`${API}/builder/drafts`)
      ) {
        listings.push(sent);
      }
    });

    await test.step('a failed listing is reported until one works', async () => {
      const LIST = '**/api/v1/builder/drafts';
      await page.route(LIST, (route) =>
        route.request().method() === 'GET'
          ? route.fulfill({
              status: 500,
              contentType: 'application/json',
              body: JSON.stringify({ message: 'simulated list failure' }),
            })
          : route.fallback(),
      );
      const failure = 'Could not list drafts. Simulated list failure.';
      await showPageAgain(page);
      await expect(errorBanner(page)).toHaveText(failure);

      // The alert can be dismissed, and a repeat is shown (and read) again.
      // Focus moves on to the tabs rather than falling to <body>.
      await page.getByTestId('builder-error-dismiss').click();
      await expect(errorBanner(page)).toHaveCount(0);
      await expect.soft(page.getByTestId('drafts-tab-mine')).toBeFocused();
      await showPageAgain(page);
      await expect(errorBanner(page)).toHaveText(failure);

      await page.unroute(LIST);
    });

    await test.step('showing the page again lists the draft', async () => {
      listings.length = 0;
      const listed = waitForApi(page, 'GET', '/builder/drafts');
      await showPageAgain(page);
      await listed;

      await expect(open).toBeVisible();
      // focus and visibilitychange together make one request.
      expect.soft(listings, 'draft listings').toHaveLength(1);
      // The name adds when the draft changed, so same-titled cards differ.
      await expect
        .soft(open)
        .toHaveAccessibleName(new RegExp(`^Open ${title}, updated \\S`));
      await expect.soft(draftCard(page, draft.id)).toContainText(title);
      // The earlier failure no longer applies.
      await expect.soft(errorBanner(page)).toHaveCount(0);
    });

    await test.step('Delete removes it from My Drafts and the server', async () => {
      const deleted = page.waitForResponse(
        (response) =>
          response.request().method() === 'DELETE' &&
          new URL(response.url()).pathname.endsWith(path),
      );
      await page.getByRole('button', { name: `Delete ${title}` }).click();
      // Named with its time, as on its card: most drafts share a title.
      await page
        .getByRole('alertdialog', {
          name: new RegExp(`^Delete draft ${title}, updated .+\\?$`),
        })
        .getByRole('button', { name: 'Delete draft' })
        .click();
      expect.soft((await deleted).ok(), 'DELETE response').toBeTruthy();

      await expect.soft(draftCard(page, draft.id)).toHaveCount(0);
      await expect
        .soft(builder.liveRegion)
        .toContainText(new RegExp(`Deleted draft ${title}, updated .+\\.`));
      await expect.soft(errorBanner(page)).toHaveCount(0);
      expect.soft((await request.get(path)).status(), 'server copy').toBe(404);
    });

    await test.step('a draft the server cannot read is listed with why, and Delete removes it', async () => {
      const unreadable = await builder.seedDraft(
        blankDocument(`${title} unreadable`),
      );
      const LIST = '**/api/v1/builder/drafts';
      // The server lists it as it lists a draft whose record it can no
      // longer read: apart, with what it can read and the ETag.
      await page.route(LIST, async (route) => {
        if (route.request().method() !== 'GET') {
          return route.fallback();
        }

        const response = await route.fetch();
        const body = await response.json();
        const listed = body.drafts.find((item) => item.id === unreadable.id);

        body.drafts = body.drafts.filter((item) => item !== listed);
        body.damaged = listed
          ? [
              {
                id: listed.id,
                owner: listed.owner,
                title: listed.title,
                updated: listed.updated,
                etag: listed.etag,
                canDelete: true,
              },
            ]
          : [];

        return route.fulfill({ response, json: body });
      });
      await showPageAgain(page);

      const card = page.getByTestId('drafts-list-mine').locator('li', {
        has: page.getByTestId(`draft-damaged-${unreadable.id}`),
      });
      await expect(card).toContainText(
        'This draft cannot be read, so it cannot be opened.',
      );
      await expect
        .soft(card.getByTestId(`draft-open-${unreadable.id}`))
        .toHaveCount(0);

      const remove = card.getByRole('button', {
        name: new RegExp(`^Delete ${title} unreadable, updated \\S`),
      });
      const deleted = page.waitForResponse(
        (response) =>
          response.request().method() === 'DELETE' &&
          new URL(response.url()).pathname.endsWith(draftPath(unreadable)),
      );
      await remove.click();
      await page
        .getByRole('alertdialog')
        .getByRole('button', { name: 'Delete draft' })
        .click();
      expect.soft((await deleted).status(), 'DELETE status').toBe(204);
      await expect(card).toHaveCount(0);
      // Its card has gone: focus moves on rather than falling to <body>.
      await expect
        .soft(page.locator('body'), 'focus after the delete')
        .not.toBeFocused();
      expect
        .soft((await request.get(draftPath(unreadable))).status())
        .toBe(404);
      await page.unroute(LIST);
    });
    expectNoFatal(issues);
  });

  test('asks for confirmation before deleting a draft', async ({
    page,
    request,
    builder,
  }, testInfo) => {
    const title = uniqueName(testInfo, 'confirm');
    const draft = await builder.seedDraft(blankDocument(title));
    await builder.open();
    await expect(draftCard(page, draft.id)).toBeVisible();

    let nativePrompt = null;
    page.on('dialog', async (prompt) => {
      nativePrompt = prompt.message();
      await prompt.dismiss();
    });
    let deleteSent = false;
    page.on('request', (sent) => {
      if (sent.method() === 'DELETE' && sent.url().includes(draft.id)) {
        deleteSent = true;
      }
    });

    await page.getByRole('button', { name: `Delete ${title}` }).click();
    const inAppPrompt = page
      .getByRole('alertdialog')
      .or(page.getByRole('dialog'));
    const prompted = async () =>
      nativePrompt !== null || (await inAppPrompt.count()) > 0;

    // Wait for a prompt or for the DELETE request, so the expected failure
    // (the draft is deleted without asking) is reported at once.
    await expect
      .poll(async () => deleteSent || (await prompted()), {
        message: 'a confirmation step or the DELETE request',
      })
      .toBe(true);
    expect(deleteSent, 'DELETE sent before any confirmation').toBe(false);
    expect(await prompted(), 'a confirmation step').toBe(true);

    // An alert dialog that names the draft, with focus on the safe choice.
    const confirm = page.getByRole('alertdialog', {
      name: new RegExp(`^Delete draft ${title}, updated .+\\?$`),
    });
    await expect(confirm).toBeVisible();
    await expect.soft(confirm).toHaveAccessibleDescription(/cannot be undone/);
    await expect(confirm.getByRole('button', { name: 'Cancel' })).toBeFocused();
    await expectAccessible(page, {
      include: '[data-testid="builder-confirm"]',
      soft: true,
    });

    // Dismissing the prompt keeps the draft and returns focus to Delete.
    const remove = page.getByRole('button', { name: `Delete ${title}` });
    await page.keyboard.press('Escape');
    await expect(confirm).toHaveCount(0);
    await expect.soft(remove).toBeFocused();

    // So does a click outside it.
    await remove.click();
    await expect(confirm).toBeVisible();
    const outside = await backdropPoint(confirm);
    await page.mouse.click(outside.x, outside.y);
    await expect(confirm).toHaveCount(0);
    await expect.soft(remove, 'focus after a click outside').toBeFocused();
    expect(deleteSent, 'DELETE sent after Cancel').toBe(false);
    const kept = await request.get(draftPath(draft));
    expect(kept.ok()).toBeTruthy();
  });
});
