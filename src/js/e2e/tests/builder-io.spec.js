// Builder import, upload, download and drafts-landing flows. The `tracker`
// fixture removes every draft, topology and experiment the tests make.

const crypto = require('crypto');

const {
  API,
  DOCUMENT_TIME,
  SCHEMA_URI,
  backdropPoint,
  blankDocument,
  deleteIcon,
  download,
  draftPath,
  errorBanner,
  expect,
  expectAccessible,
  expectDetail,
  expectNoFatal,
  iconName,
  iconOf,
  isPublishResponse,
  labDocument,
  openUpload,
  ownColor,
  pngOf,
  publishTopology,
  seedConfig,
  seedIcon,
  test,
  uniqueName,
  visit,
  waitForApi,
  watchDraftCreates,
} = require('./builder-support');

const API_VERSION = 'phenix.sandia.gov/v1';

// --- local helpers -----------------------------------------------------------

// What the page sees when the user comes back to its browser tab or window:
// the document becomes visible and the window takes focus.
async function showPageAgain(page) {
  await page.evaluate(() => {
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('focus'));
  });
}

// A phenix v1 topology node. `interfaces` is a list of [name, vlan] pairs.
// Addresses derive from the hostname, as publishing refuses duplicates.
function topologyNode(hostname, interfaces, extra = {}) {
  const host = [...hostname].reduce(
    (sum, character) => (sum * 31 + character.charCodeAt(0)) % 65521,
    7,
  );

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
        address: `10.${index}.${host >> 8}.${host & 255}`,
        mask: 24,
        proto: 'static',
        type: 'ethernet',
      })),
    },
  };
}

// Vue Flow's wrapper around the diagram's one switch: the focusable element,
// which the switch's info (its connected devices) describes.
async function switchWrapper(page, builder) {
  const id = await builder.nodes('switch').getAttribute('data-node-id');

  return page.locator(`.vue-flow__node[data-id="${id}"]`);
}

function topologyConfig(name, nodes) {
  return {
    apiVersion: API_VERSION,
    kind: 'Topology',
    metadata: { name },
    spec: { nodes },
  };
}

// host-a and host-b share VLAN EXP. host-b also sits on MGMT.
function sharedVlanNodes() {
  return [
    topologyNode('host-a', [['eth0', 'EXP']]),
    topologyNode('host-b', [
      ['eth0', 'EXP'],
      ['eth1', 'MGMT'],
    ]),
  ];
}

// Publishes a one-device (pub-host) diagram, generated as Import does, as
// topology `name`, then deletes its draft so it opens in a new draft.
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
    keepDraft: false,
  });

  return { name, ...published };
}

// Creates an experiment from a stored topology and an optional scenario.
// Outside CI, it skips the test when the server cannot create experiments.
async function createExperiment(request, tracker, name, topology, scenario) {
  const created = await request.post(`${API}/experiments`, {
    data: { name, topology, ...(scenario ? { scenario } : {}) },
  });
  tracker.config('Experiment', name);
  if (!created.ok()) {
    const reason = `Experiment creation is unavailable on this server: ${created.status()} ${await created.text()}`;
    if (process.env.CI) {
      throw new Error(reason);
    }
    test.skip(true, reason);
  }

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

// Stores an experiment (aliases EXP 101, MGMT 102) of a topology including
// `child`, which includes `nested`, with a scenario of apps the server lacks.
async function storedExperiment(request, tracker, testInfo) {
  const topology = uniqueName(testInfo, 'gen-exp-topo');
  const experiment = uniqueName(testInfo, 'gen-exp');
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
  // phenix uses a scenario only with the topology it names.
  const scenario = uniqueName(testInfo, 'gen-exp-scn');
  await seedConfig(request, tracker, {
    apiVersion: 'phenix.sandia.gov/v2',
    kind: 'Scenario',
    metadata: { name: scenario, annotations: { topology } },
    spec: {
      apps: [
        {
          name: 'e2e-traffic',
          hosts: [{ hostname: 'host-a' }, { hostname: 'host-b' }],
        },
        { name: 'e2e-monitor' },
      ],
    },
  });
  await createExperiment(request, tracker, experiment, topology, scenario);
  await setExperimentAliases(request, experiment, { EXP: 101, MGMT: 102 });

  return { topology, experiment, child, nested, scenario };
}

// Vue Flow's wrapper around the one canvas node of `kind`: its Tab stop.
function flowNode(builder, kind) {
  return builder.page
    .locator('.vue-flow__node')
    .filter({ has: builder.nodes(kind) });
}

// Switch EXP and device `node` connected by eth0, placed so the line runs
// round, left of the switch, into the switch's left side.
function connectedDiagram(title) {
  const doc = labDocument(title, { hostnames: ['node'] });
  const at = (kind) => doc.nodes.find((node) => node.kind === kind);

  at('switch').position = { x: 80, y: 80 };
  at('device').position = { x: 300, y: 240 };

  return doc;
}

// Seeds a draft of connectedDiagram(title), and opens it. Returns the
// draft.
async function openConnectedDraft(builder, title) {
  const draft = await builder.seedDraft(connectedDiagram(title));
  await builder.openDraft(draft);

  return draft;
}

// Checks a download from the Download dialog of the diagram `title`: its
// file name, the status line it leaves and the absence of an error.
function savedChecker(dialog, title) {
  const status = dialog.getByRole('status');
  const downloadError = dialog.getByTestId('download-error');

  return async (file, extension) => {
    const fileName = downloadFileName(title, extension);
    expect.soft(file.name, `${extension} file name`).toBe(fileName);
    await expect.soft(status).toHaveText(`Saved ${fileName}.`);
    await expect
      .soft(downloadError, `${extension} download error`)
      .toHaveCount(0);
  };
}

// Width and height from a PNG's IHDR chunk.
function pngSize(buffer) {
  return { width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20) };
}

// connectedDiagram() as the canvas draws it: line stroke and width, points
// just outside each line end and on its left run, and the device's fill.
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

// Soft-checks a downloaded PNG or SVG, as the browser draws it, against
// `diagram`. `origin` is the flow point at the image's top-left corner.
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

// Soft-checks a downloaded SVG's markup: inline line style, no editing
// affordances or selection, and nothing hidden or inert.
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

function downloadFileName(title, extension) {
  const base = title
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return `${base || 'topology'}.${extension}`;
}

// Each dialog is found by its title, so a button that opens the other fails.
async function openImport(page) {
  await page.getByTestId('drafts-import').click();
  const dialog = page.getByRole('dialog', {
    name: 'Import topology or experiment',
  });
  await expect(dialog).toBeVisible();

  return dialog;
}

// Sends `content` through Import > Config file, submits it and returns the
// dialog and the POST /builder/generate response.
async function importFromFile(page, content, fileName = 'topology.yaml') {
  const dialog = await openImport(page);
  await dialog.getByLabel('Config file', { exact: true }).check();
  await dialog.getByTestId('import-file').setInputFiles({
    name: fileName,
    mimeType: fileName.endsWith('.json')
      ? 'application/json'
      : 'application/yaml',
    buffer: Buffer.from(content),
  });
  await expect(dialog.getByTestId('import-submit')).toBeEnabled();

  const generated = waitForApi(page, 'POST', '/builder/generate');
  await dialog.getByTestId('import-submit').click();

  return { dialog, response: await generated };
}

// Continues past Import's warnings, if any. Without them the dialog closes.
async function continuePastWarnings(dialog) {
  const next = dialog.getByTestId('import-continue');
  await expect
    .poll(async () => (await next.isVisible()) || !(await dialog.isVisible()))
    .toBe(true);
  if (await next.isVisible()) {
    await next.click();
  }
}

function draftCard(page, id) {
  return page
    .getByTestId('drafts-list-drafts')
    .locator('li', { has: page.getByTestId(`draft-open-${id}`) });
}

// --- download and upload -----------------------------------------------------

test.describe('download and upload', () => {
  test(
    'shows the formats in two rows, and downloads JSON, YAML, Topology YAML, draw.io and GEXF',
    {
      tag: '@cross-browser',
    },
    async ({ page, request, builder, issues }, testInfo) => {
      const title = uniqueName(testInfo, 'download');
      const original = await openConnectedDraft(builder, title);
      const saved = await builder.serverDocument(original);
      const labels = saved.nodes.map((node) => node.label);
      expect(labels).toHaveLength(2);

      const dialog = await builder.openDialog('download');
      const status = dialog.getByRole('status');
      const downloadError = dialog.getByTestId('download-error');
      const expectSaved = savedChecker(dialog, title);
      // The status region is there, empty, so the first message is announced.
      await expect.soft(status).toHaveText('');

      await test.step('the formats are in two rows', async () => {
        const rows = [
          ['json', 'yaml', 'topology-yaml'],
          ['png', 'svg', 'drawio', 'gexf'],
        ];
        const boxes = {};
        for (const format of rows.flat()) {
          boxes[format] = await dialog
            .getByTestId(`download-${format}`)
            .boundingBox();
        }
        for (const row of rows) {
          expect
            .soft(
              row.map((format) =>
                Math.round(boxes[format].y - boxes[row[0]].y),
              ),
              `tops of ${row}`,
            )
            .toEqual(row.map(() => 0));
        }
        expect
          .soft(boxes.png.y, 'PNG is under Builder JSON')
          .toBeGreaterThanOrEqual(boxes.json.y + boxes.json.height);
        expect.soft(boxes.png.x, 'the rows start together').toBe(boxes.json.x);
      });

      await test.step('the JSON download is the saved document', async () => {
        const file = await download(page, () =>
          dialog.getByTestId('download-json').click(),
        );
        await expectSaved(file, 'json');

        const downloaded = JSON.parse(file.buffer.toString('utf8'));
        expect.soft(downloaded.$schema).toBe(SCHEMA_URI);
        expect.soft(downloaded.revision).toBe(1);
        expect.soft(downloaded.metadata.name).toBe(title);
        expect
          .soft(downloaded.nodes.map((node) => node.kind).sort())
          .toEqual(['device', 'switch']);
        expect
          .soft(downloaded.networks.map((network) => network.name))
          .toEqual(['EXP']);
        expect.soft(downloaded.edges).toHaveLength(1);
        expect.soft(downloaded.metadata.id).toBe(saved.metadata.id);
        expect.soft(downloaded.nodes).toEqual(saved.nodes);
        expect.soft(downloaded.edges).toEqual(saved.edges);
      });

      await test.step('the YAML download has the document keys', async () => {
        const file = await download(page, () =>
          dialog.getByTestId('download-yaml').click(),
        );
        await expectSaved(file, 'yaml');

        // The e2e package has no YAML parser. Check the top-level keys by line
        // and let the product's strict importer prove the document parses.
        const text = file.buffer.toString('utf8');
        expect
          .soft(text)
          .toMatch(
            /^\$schema: https:\/\/phenix\.sandia\.gov\/schemas\/builder\/v1$/m,
          );
        expect.soft(text).toMatch(/^revision: 1$/m);
        expect.soft(text).toMatch(/^metadata:$/m);
        expect.soft(text).toContain(`\n  name: ${title}\n`);
        expect.soft(text).toMatch(/^nodes:$/m);
        expect.soft(text.match(/^ {4}kind: device$/gm)).toHaveLength(1);
        expect.soft(text.match(/^ {4}kind: switch$/gm)).toHaveLength(1);
      });

      await test.step('the Topology YAML download is the topology Publish would write', async () => {
        const file = await download(page, () =>
          dialog.getByTestId('download-topology-yaml').click(),
        );
        await expectSaved(file, 'topology.yaml');
        await expect
          .soft(dialog.getByTestId('download-topology-yaml'))
          .not.toHaveAttribute('aria-busy', 'true');

        const text = file.buffer.toString('utf8');
        expect.soft(text).toMatch(/^apiVersion: phenix\.sandia\.gov\/v1$/m);
        expect.soft(text).toMatch(/^kind: Topology$/m);
        expect.soft(text).toContain(`\nmetadata:\n    name: ${title}\n`);

        // The server's importer parses it as a phenix Topology, and finds
        // the diagram's device, connected to its network.
        const imported = await request.post(`${API}/builder/generate`, {
          data: { content: text },
        });
        expect(imported.ok(), await imported.text()).toBeTruthy();
        const { document } = await imported.json();
        const devices = (doc) =>
          doc.nodes
            .filter((node) => node.kind === 'device')
            .map((node) => node.device.hostname);
        expect.soft(devices(document)).toEqual(devices(saved));
        expect
          .soft(document.networks.map((network) => network.name))
          .toEqual(['EXP']);
      });

      await test.step('the draw.io download is the diagram as a draw.io page', async () => {
        const button = dialog.getByTestId('download-drawio');
        await expect.soft(button).toHaveAccessibleName('draw.io');
        await expect
          .soft(button)
          .toHaveAccessibleDescription(/as a drawing to edit in draw\.io/);
        const file = await download(page, () => button.click());
        await expectSaved(file, 'drawio');

        // The browser's XML parser reads it: a cell for each node and
        // connection, under the page's own two cells, with the Builder ids.
        const drawing = await page.evaluate((text) => {
          const xml = new DOMParser().parseFromString(text, 'application/xml');
          const ids = [...xml.getElementsByTagName('mxCell')].map((cell) =>
            (cell.parentElement.tagName === 'object'
              ? cell.parentElement
              : cell
            ).getAttribute('id'),
          );

          return {
            errors: xml.getElementsByTagName('parsererror').length,
            root: xml.documentElement.tagName,
            host: xml.documentElement.getAttribute('host'),
            first: ids.slice(0, 2),
            ids: ids.slice(2).sort(),
            edges: xml.querySelectorAll('mxCell[edge="1"][source][target]')
              .length,
          };
        }, file.buffer.toString('utf8'));
        expect.soft(drawing).toEqual({
          errors: 0,
          root: 'mxfile',
          host: 'phenix',
          first: ['0', '1'],
          ids: [...saved.nodes, ...saved.edges].map((item) => item.id).sort(),
          edges: saved.edges.length,
        });
      });

      await test.step('the GEXF download is the network as a graph for Gephi', async () => {
        const fileName = downloadFileName(title, 'gexf');
        const button = dialog.getByTestId('download-gexf');
        await expect.soft(button).toHaveAccessibleName('Gephi (GEXF)');
        await expect
          .soft(button)
          .toHaveAccessibleDescription(/as a graph to analyze in Gephi/);
        const file = await download(page, () => button.click());
        expect.soft(file.name).toBe(fileName);
        await expect
          .soft(status)
          .toHaveText(
            `Saved ${fileName}: 1 device, 1 network and 1 connection.`,
          );
        await expect.soft(downloadError).toHaveCount(0);

        // The browser's XML parser reads it: one node for each of the
        // diagram's nodes, each placed, and its connection.
        const graph = await page.evaluate((text) => {
          const xml = new DOMParser().parseFromString(text, 'application/xml');
          const gexf = 'http://gexf.net/1.3';
          const nodes = [...xml.getElementsByTagNameNS(gexf, 'node')];

          return {
            errors: xml.getElementsByTagName('parsererror').length,
            root: [
              xml.documentElement.namespaceURI,
              xml.documentElement.localName,
            ],
            version: xml.documentElement.getAttribute('version'),
            labels: nodes.map((node) => node.getAttribute('label')).sort(),
            positions: xml.getElementsByTagNameNS(`${gexf}/viz`, 'position')
              .length,
            edges: xml.getElementsByTagNameNS(gexf, 'edge').length,
          };
        }, file.buffer.toString('utf8'));
        expect.soft(graph).toEqual({
          errors: 0,
          root: ['http://gexf.net/1.3', 'gexf'],
          version: '1.3',
          labels: [...labels].sort(),
          positions: saved.nodes.length,
          edges: saved.edges.length,
        });
      });
      expectNoFatal(issues);
    },
  );

  test(
    'downloads PNG and SVG images of the whole diagram, with nothing selected, in each theme',
    {
      tag: '@cross-browser',
    },
    async ({ page, builder, issues }, testInfo) => {
      const title = uniqueName(testInfo, 'download');
      const original = await openConnectedDraft(builder, title);
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

      // An image shows the diagram, not the view: zoom in and select all.
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

      const dialog = await builder.openDialog('download');
      const expectSaved = savedChecker(dialog, title);

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

      await test.step('the PNG download covers the whole diagram', async () => {
        // By keyboard: the button is busy while the image renders, and must
        // keep focus throughout rather than drop it outside the dialog.
        const button = dialog.getByTestId('download-png');
        const png = await download(page, () => button.press('Enter'));
        await expectSaved(png, 'png');
        await expect.soft(button).toBeFocused();
        // The image is drawn from a copy of the canvas, removed after. The
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

      await test.step('the SVG download draws every node label and the connection', async () => {
        const svg = await download(page, () =>
          dialog.getByTestId('download-svg').click(),
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

      await test.step('PNG and SVG downloads draw the diagram in the dark theme', async () => {
        await useColorScheme(page, 'dark');
        const png = await download(page, () =>
          dialog.getByTestId('download-png').click(),
        );
        await expectSaved(png, 'png');
        await expectDiagramInImage(page, 'PNG', png.buffer, dark, imageArea);
        const svg = await download(page, () =>
          dialog.getByTestId('download-svg').click(),
        );
        await expectSaved(svg, 'svg');
        expectDiagramInSvg(svg.buffer.toString('utf8'), dark);
        await expectDiagramInImage(page, 'SVG', svg.buffer, dark, imageArea);
        await useColorScheme(page, 'light');
      });

      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(builder.dialog).toBeHidden();
      expectNoFatal(issues);
    },
  );

  test(
    'uploads the downloaded Builder files, JSON in the editor and YAML on the drafts landing, as new drafts',
    {
      tag: '@cross-browser',
    },
    async ({ page, builder, issues }, testInfo) => {
      const title = uniqueName(testInfo, 'upload');
      const original = await openConnectedDraft(builder, title);
      const summary = await builder.summary.textContent();
      const saved = await builder.serverDocument(original);

      // The Builder files, as the Download dialog saves them.
      const dialog = await builder.openDialog('download');
      const jsonFile = await download(page, () =>
        dialog.getByTestId('download-json').click(),
      );
      const json = {
        file: jsonFile,
        downloaded: JSON.parse(jsonFile.buffer.toString('utf8')),
      };
      const yaml = await download(page, () =>
        dialog.getByTestId('download-yaml').click(),
      );
      await dialog.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(builder.dialog).toBeHidden();

      const jsonCopy =
        await test.step('the JSON file uploads from the editor toolbar as a new draft', async () => {
          // Named as on the drafts page: Import there converts a config.
          await expect
            .soft(builder.toolbar('upload'))
            .toHaveAccessibleName('Upload');
          const uploadDialog = await builder.openDialog('upload');
          await expect
            .soft(uploadDialog)
            .toHaveAccessibleName('Upload diagram');
          await expect
            .soft(uploadDialog.getByTestId('upload-submit'))
            .toHaveText('Upload');
          await uploadDialog.getByTestId('upload-file').setInputFiles({
            name: json.file.name,
            mimeType: 'application/json',
            buffer: json.file.buffer,
          });
          const created = waitForApi(page, 'POST', '/builder/drafts');
          await uploadDialog.getByTestId('upload-submit').click();
          const uploaded = await (await created).json();

          await expect(builder.dialog).toBeHidden();
          expect.soft(uploaded.id, 'a new draft').not.toBe(original.id);
          await expect.soft(builder.summary).toHaveText(summary);
          await expect.soft(page.getByTestId('builder-name')).toHaveText(title);
          await builder.waitSaved();

          const copy = await builder.serverDocument(uploaded);
          expect.soft(copy.nodes).toEqual(json.downloaded.nodes);
          // The server drops empty optional fields: compare stored copies.
          expect.soft(copy.networks).toEqual(saved.networks);
          expect.soft(copy.edges).toEqual(json.downloaded.edges);
          expect
            .soft(await builder.serverDocument(original), 'the original draft')
            .toEqual(saved);

          return uploaded;
        });

      await builder.backToDrafts();
      await expect
        .soft(page.getByTestId(`draft-open-${original.id}`))
        .toBeVisible();
      await expect
        .soft(page.getByTestId(`draft-open-${jsonCopy.id}`))
        .toBeVisible();

      await test.step('the YAML file uploads from the drafts landing as a new draft', async () => {
        const uploadDialog = await openUpload(page);
        await expect
          .soft(uploadDialog.getByLabel('File', { exact: true }))
          .toBeChecked();
        await uploadDialog.getByTestId('upload-file').setInputFiles({
          name: yaml.name,
          mimeType: 'text/yaml',
          buffer: yaml.buffer,
        });
        const created = waitForApi(page, 'POST', '/builder/drafts');
        await uploadDialog.getByTestId('upload-submit').click();
        const uploaded = await (await created).json();

        expect.soft(uploaded.id, 'a new draft').not.toBe(original.id);
        await expect(builder.canvas).toBeVisible();
        await expect.soft(builder.summary).toHaveText(summary);
        await expect.soft(page.getByTestId('builder-name')).toHaveText(title);
        await builder.waitSaved();
        expect
          .soft((await builder.serverDocument(uploaded)).nodes)
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

    let dialog = await openUpload(page);
    const error = dialog.getByTestId('upload-error');

    // Each refusal names the problem, marks the field and moves focus to it.
    await test.step('no file, then an oversized file', async () => {
      const file = dialog.getByTestId('upload-file');
      const tooLarge = 'The uploaded file is larger than the 5 MiB limit.';

      await dialog.getByTestId('upload-submit').click();
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
      // handler runs in the click task. A later task sees its DOM update.
      await dialog.getByTestId('upload-submit').click();
      await page.evaluate(() => null);
      expect.soft(await error.textContent()).toBe(tooLarge);
      await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
      expect(creates).toEqual([]);
    });

    // The Topology and Experiment refusals name the kind, with its article,
    // and point at Import on the drafts page.
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
        /^The Builder could not parse the document: .+ \(line \d+, column \d+\)$/,
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
        /^A Topology config is not a Builder document\. Select Import on the Drafts page\b/,
      ],
      [
        'an Experiment config',
        JSON.stringify({
          apiVersion: API_VERSION,
          kind: 'Experiment',
          metadata: { name },
          spec: {},
        }),
        /^An Experiment config is not a Builder document\. Select Import on the Drafts page\b/,
      ],
      // A template file goes to Import templates, not to a draft.
      [
        'a node template file',
        [
          'name: Plant',
          'templates:',
          '  - name: PLC',
          '    device:',
          '      iconKey: server',
          '',
        ].join('\n'),
        'This is a node template file, not a diagram. To add its templates to the library, select Import templates on the Node Templates tab.',
      ],
      // A layer set file goes to Upload layer set, not to a draft.
      [
        'a layer set file',
        [
          '$schema: https://phenix.sandia.gov/schemas/builder/layersets/v1',
          'name: Plant',
          'layers:',
          '  - id: 1',
          '    label: Control',
          '',
        ].join('\n'),
        'This is a layer set file, not a diagram. To add it to the library, select Upload layer set on the Layers tab.',
      ],
    ];

    await dialog.getByLabel('Paste text', { exact: true }).check();
    for (const [what, text, message] of refusals) {
      await test.step(`pasted ${what}`, async () => {
        const field = dialog.getByTestId('upload-text');
        await field.fill(text);
        await dialog.getByTestId('upload-submit').click();
        await expect.soft(error, what).toHaveText(message);
        await expect.soft(field, what).toHaveAttribute('aria-invalid', 'true');
        await expect.soft(field, what).toBeFocused();
        // An accepted input closes the dialog and creates a draft. Stop here
        // rather than wait out the next case's fill.
        await expect(dialog, what).toBeVisible();
        expect(creates, what).toEqual([]);
      });
    }

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
    await expect
      .soft(page.getByRole('heading', { name: 'Builder', exact: true }))
      .toBeVisible();
    expect.soft(creates, 'draft creates after Cancel').toEqual([]);

    await test.step('a pasted builder document opens as a new draft', async () => {
      const title = uniqueName(testInfo, 'paste-ok');
      const document = blankDocument(title);

      dialog = await openUpload(page);
      await dialog.getByLabel('Paste text', { exact: true }).check();
      await dialog.getByTestId('upload-text').fill(JSON.stringify(document));

      const created = waitForApi(page, 'POST', '/builder/drafts');
      await dialog.getByTestId('upload-submit').click();
      const response = await created;
      const draft = await response.json();

      await expect(dialog).toBeHidden();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(page.getByTestId('builder-name')).toHaveText(title);
      // Pasted text has no file name to record.
      expect
        .soft(response.request().postDataJSON())
        .not.toHaveProperty('sourceFile');
      expect.soft(draft.sourceFile, 'sourceFile').toBeUndefined();
      await expect
        .soft(builder.inspector.getByTestId('inspector-source-file'))
        .toHaveCount(0);
      await expect
        .soft(builder.summary)
        .toContainText('0 devices, 0 switches, 0 networks');
      await builder.waitSaved();

      expect.soft((await builder.serverDraft(draft)).title).toBe(title);
      const doc = await builder.serverDocument(draft);
      expect.soft(doc).toMatchObject({
        $schema: SCHEMA_URI,
        metadata: { id: document.metadata.id, name: title },
      });
    });
    expectNoFatal(issues);
  });

  test('an uploaded file keeps the creator it names, shows the uploader as its last editor, and is named in Details', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const title = uniqueName(testInfo, 'claimed');
    const fileName = `${title}.builder.json`;
    const claimed = {
      createdBy: 'alice',
      createdAt: '2020-01-02T03:04:05Z',
      updatedBy: 'alice',
      updatedAt: '2020-02-03T04:05:06Z',
    };
    const blank = blankDocument(title);

    await builder.open();
    const dialog = await openUpload(page);
    await dialog.getByTestId('upload-file').setInputFiles({
      name: fileName,
      mimeType: 'application/json',
      buffer: Buffer.from(
        JSON.stringify({
          ...blank,
          metadata: { ...blank.metadata, ...claimed },
        }),
      ),
    });
    const answered = waitForApi(page, 'POST', '/builder/drafts');
    await dialog.getByTestId('upload-submit').click();
    const response = await answered;
    const draft = await response.json();
    await expect(builder.canvas).toBeVisible();
    await builder.waitSaved();

    await test.step('the draft records the name of the file', async () => {
      expect.soft(response.request().postDataJSON().sourceFile).toBe(fileName);
      expect.soft(draft.sourceFile).toBe(fileName);
      expect.soft((await builder.serverDraft(draft)).sourceFile).toBe(fileName);
      const row = builder.inspector.getByTestId('inspector-source-file');
      await expect.soft(row.locator('dt')).toHaveText('Source file');
      await expect.soft(row.locator('dd')).toHaveText(fileName);
    });

    await test.step('the creator the file names is kept, and the uploader edited it last', async () => {
      expect(draft.stamp).toEqual({
        createdBy: claimed.createdBy,
        createdAt: claimed.createdAt,
        updatedBy: draft.createdBy,
        updatedAt: expect.stringMatching(DOCUMENT_TIME),
      });
      expect.soft(draft.stamp.updatedAt).not.toBe(claimed.updatedAt);
      await expectDetail(page, 'created', claimed.createdBy, claimed.createdAt);
      await expectDetail(
        page,
        'edited',
        draft.createdBy,
        draft.stamp.updatedAt,
      );
    });

    await test.step('a save keeps both, and the file name', async () => {
      const saved = builder.nextSnapshot();
      await builder.rename(`${title} renamed`);
      const { stamp, sourceFile } = await (await saved).json();
      expect.soft(stamp).toMatchObject({
        createdBy: claimed.createdBy,
        createdAt: claimed.createdAt,
        updatedBy: draft.createdBy,
      });
      expect.soft(sourceFile).toBe(fileName);
      await expectDetail(page, 'created', claimed.createdBy, claimed.createdAt);
      await expect
        .soft(builder.inspector.getByTestId('inspector-source-file'))
        .toContainText(fileName);
      await builder.waitSaved();
    });

    expectNoFatal(issues);
  });

  // A download carries copies of the custom icons it names, and an upload adds
  // those the server lacks. The tracker deletes the test's icons.
  test('a download carries copies of the icons the diagram names, and an upload adds those the server lacks', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const title = uniqueName(testInfo, 'icons');
    // On the server, and named by a device.
    const ours = iconOf(pngOf(16, 8, ownColor()), iconName('io-ours'));
    // Named by a device, and on no server.
    const gone = iconOf(pngOf(8, 8, ownColor()), iconName('io-gone'));
    // On the server with other bytes than the file's copy has.
    const clash = iconOf(pngOf(8, 16, ownColor()), iconName('io-clash'));
    const theirs = iconOf(pngOf(8, 16, ownColor()), clash.name);
    const device = (hostname, icon, x) => ({
      id: crypto.randomUUID(),
      kind: 'device',
      label: hostname,
      position: { x, y: 64 },
      device: {
        hostname,
        iconKey: 'linux',
        icon: icon.name,
        spec: {
          type: 'VirtualMachine',
          general: { hostname, vm_type: 'kvm' },
          hardware: { os_type: 'linux', drives: [{ image: 'ubuntu.qc2' }] },
          network: { interfaces: [] },
        },
        interfaces: [],
      },
    });

    await seedIcon(request, tracker, ours);
    await seedIcon(request, tracker, clash);

    const draft = await builder.seedDraft(
      blankDocument(title, {
        nodes: [device('ours-01', ours, 64), device('gone-01', gone, 320)],
      }),
    );

    await builder.openDraft(draft);
    await expect(
      builder.node('ours-01', 'device').locator('img.builder-icon--custom'),
    ).toHaveAttribute('src', `data:image/png;base64,${ours.data}`);
    // A name nothing resolves draws the built-in icon.
    await expect
      .soft(
        builder.node('gone-01', 'device').locator('img.builder-icon--custom'),
      )
      .toHaveCount(0);

    const downloaded =
      await test.step('the JSON download carries a copy of each icon the server has, and says which it lacks', async () => {
        const dialog = await builder.openDialog('download');
        const file = await download(page, () =>
          dialog.getByTestId('download-json').click(),
        );
        const doc = JSON.parse(file.buffer.toString('utf8'));

        expect(doc.icons).toEqual({ [ours.name]: { data: ours.data } });
        await expect
          .soft(dialog.getByRole('status'))
          .toHaveText(
            `Saved ${file.name}. It does not carry the custom icon ${gone.name}. The server's icon library does not have it. The nodes that name it show their built-in icon.`,
          );
        // The draft itself still carries no image.
        expect
          .soft(await builder.serverDocument(draft))
          .not.toHaveProperty('icons');
        await dialog
          .getByRole('button', { name: 'Close', exact: true })
          .click();

        return doc;
      });

    await test.step('an upload adds the copy the server lacks, and keeps one whose name the server has for another image, with a warning', async () => {
      // The server loses ours. The file carries a copy of clash that is
      // not the server's.
      expect((await deleteIcon(request, ours.name)).status()).toBe(204);

      const file = {
        ...downloaded,
        metadata: {
          ...downloaded.metadata,
          id: crypto.randomUUID(),
          name: `${title} uploaded`,
        },
        nodes: downloaded.nodes.map((node) =>
          node.device?.hostname === 'gone-01'
            ? { ...node, device: { ...node.device, icon: clash.name } }
            : node,
        ),
        icons: { ...downloaded.icons, [clash.name]: theirs.entry },
      };

      await builder.open();
      const dialog = await openUpload(page);

      await dialog.getByTestId('upload-file').setInputFiles({
        name: `${title}.builder.json`,
        mimeType: 'application/json',
        buffer: Buffer.from(JSON.stringify(file)),
      });

      const added = waitForApi(page, 'POST', '/builder/icons');

      await dialog.getByTestId('upload-submit').click();

      const response = await added;

      expect(response.status()).toBe(201);
      expect.soft(response.request().postDataJSON()).toEqual({
        name: ours.name,
        data: ours.data,
      });

      // The warning comes before any draft is made.
      const warnings = dialog.getByTestId('upload-warnings');

      await expect(warnings).toHaveText(
        `Warning: The server already has an icon named ${clash.name} that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.`,
      );
      await expect
        .soft(dialog.getByTestId('upload-status'))
        .toHaveText('This upload has 1 warning.');
      await expect.soft(dialog.getByTestId('upload-continue')).toBeFocused();

      const created = waitForApi(page, 'POST', '/builder/drafts');

      await dialog.getByTestId('upload-continue').click();

      const uploaded = await (await created).json();

      await expect(builder.canvas).toBeVisible();
      await builder.waitSaved();
      // Only the copy the server cannot take as it is stays in the draft.
      await builder.persisted(uploaded, (doc) => doc.icons ?? null, {
        [clash.name]: theirs.entry,
      });
      // The copy wins over the server's icon of its name.
      await expect(
        builder.node('gone-01', 'device').locator('img.builder-icon--custom'),
      ).toHaveAttribute('src', `data:image/png;base64,${theirs.data}`);
      await expect(
        builder.node('ours-01', 'device').locator('img.builder-icon--custom'),
      ).toHaveAttribute('src', `data:image/png;base64,${ours.data}`);

      // The server has ours again, as the uploader's.
      const back = await request.get(`${API}/builder/icons/${ours.name}`);

      expect.soft(back.status()).toBe(200);
      expect.soft((await back.json()).data).toBe(ours.data);
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
    const deepLink = `/builder?topology=${encodeURIComponent(name)}`;
    const creates = watchDraftCreates(page);

    await test.step('a deep link to a topology that does not exist shows an error', async () => {
      await visit(page, deepLink);
      await expect(
        page.getByRole('heading', { name: 'Builder', exact: true }),
      ).toBeVisible({ timeout: 20000 });
      await expect
        .soft(errorBanner(page))
        .toHaveText(
          `Topology ${name} does not exist, or you do not have permission to read it.`,
        );
      await expect.soft(builder.canvas).toHaveCount(0);
      await expect.soft(page.getByRole('dialog')).toHaveCount(0);
      await expect.soft(page.getByTestId('drafts-blank')).toBeEnabled();
      expect.soft(creates, 'draft creates').toEqual([]);
    });

    // Each path opens a diagram with no draft yet, so each makes a new draft.
    const linked = await publishDiagram(request, tracker, name);
    const chosen = await publishDiagram(
      request,
      tracker,
      uniqueName(testInfo, 'published-upload'),
    );

    async function expectOpened(draft, published) {
      expect.soft(draft.id, 'a new draft').not.toBe(published.draft.id);
      await expect(builder.canvas).toBeVisible({ timeout: 20000 });
      await expect
        .soft(page.getByTestId('builder-name'))
        .toHaveText(published.name);
      await expect
        .soft(builder.summary)
        .toContainText('1 device, 1 switch, 1 network, 1 connection');
      await expect.soft(builder.canvasItem('pub-host')).toBeVisible();
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
      const dialog = await openUpload(page);
      await dialog.getByLabel('Published diagram', { exact: true }).check();
      const choice = dialog.getByTestId('upload-published');
      await expect.soft(choice).toHaveValue('');
      // A published diagram is on the server already: it is opened.
      await expect
        .soft(dialog.getByTestId('upload-submit'))
        .toHaveAccessibleName('Open');
      const opened = creates.length;
      await dialog.getByTestId('upload-submit').click();
      await expect
        .soft(dialog.getByTestId('upload-error'))
        .toHaveText('Select a published diagram.');
      await expect(dialog).toBeVisible();
      expect.soft(creates, 'draft creates').toHaveLength(opened);

      await choice.selectOption({ label: chosen.name });
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await dialog.getByTestId('upload-submit').click();
      await expectOpened(await (await created).json(), chosen);
    });
    expectNoFatal(issues);
  });
});

// --- import ------------------------------------------------------------------

test.describe('import', () => {
  test('imports a draft from a stored topology; a kind with no configs shows an empty state', async ({
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

    // Answer the sources with the real catalog minus its experiments, which
    // other specs may leave on a shared server.
    await page.route(`**${API}/builder/sources`, async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      await route.fulfill({ response, json: { ...body, experiments: [] } });
    });
    await builder.open();
    // Stored after the landing page read the list: Import reads it again
    // when it opens.
    const gone = uniqueName(testInfo, 'gen-gone');
    await seedConfig(request, tracker, topologyConfig(gone, []));

    const dialog = await openImport(page);
    const kind = dialog.getByTestId('import-kind');
    const choices = dialog.getByTestId('import-name');
    const empty = dialog.getByTestId('import-empty');
    const submit = dialog.getByTestId('import-submit');
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
        .soft(dialog.getByTestId('import-error'))
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
      // test), and nothing can be imported.
      await expect.soft(choices.locator('option')).toHaveCount(1);
      await expect.soft(choices).toHaveValue('');
      await expect.soft(submit).toBeDisabled();
    });

    await kind.selectOption('topology');
    await expect.soft(empty).toHaveCount(0);
    await expect(choices.locator('option', { hasText: name })).toHaveCount(1);

    await test.step('the options show only for a topology they apply to', async () => {
      const includes = dialog.getByTestId('import-includes');
      const copy = dialog.getByTestId('import-copy');

      // Nothing is chosen yet.
      await expect.soft(includes).toHaveCount(0);
      await expect.soft(copy).toHaveCount(0);

      // A topology that includes none can only be copied.
      await choices.selectOption(nested);
      await expect.soft(copy).toBeVisible();
      await expect.soft(copy).not.toBeChecked();
      await expect.soft(includes).toHaveCount(0);

      // One that includes some is imported with them read only, unless the
      // user chooses otherwise. Focus stays on the control that was used.
      await choices.selectOption(name);
      await expect.soft(choices).toBeFocused();
      await expect
        .soft(dialog.getByRole('group', { name: 'Included topologies' }))
        .toBeVisible();
      await expect
        .soft(includes)
        .toContainText(`${name} includes 1 other topology.`);
      await expect
        .soft(dialog.getByLabel('Keep included nodes read only'))
        .toBeChecked();
      await expect
        .soft(dialog.getByLabel('Combine into one new topology'))
        .not.toBeChecked();
      await expect.soft(copy).not.toBeChecked();
      await expect.soft(dialog.getByTestId('import-new-name')).toHaveCount(0);
    });

    const generated = waitForApi(page, 'POST', '/builder/generate');
    const created = waitForApi(page, 'POST', '/builder/drafts');
    await submit.click();
    // The choice that was shown is sent as it stood.
    expect
      .soft((await generated).request().postDataJSON())
      .toEqual({ source: `topology/${name}`, includes: 'keep' });
    const result = await (await generated).json();

    // The nested include is followed, and the warning says what was added.
    const added =
      `The Builder added 3 nodes from included topologies ${child} (2 nodes) and ` +
      `${nested} (1 node). The diagram shows them read only`;
    expect.soft(result.warnings, 'import warnings').toHaveLength(1);
    expect.soft(result.warnings[0]).toContain(added);
    await expect
      .soft(dialog.getByTestId('import-warnings'))
      .toContainText(added);
    await continuePastWarnings(dialog);
    const draft = await (await created).json();

    await expect(dialog).toBeHidden();
    await expect.soft(page.getByTestId('builder-name')).toHaveText(name);
    await expect
      .soft(builder.summary)
      .toContainText(
        '5 devices, 3 switches, 3 networks, 6 connections, 0 groups, 0 notes',
      );
    await expect.soft(builder.nodes('device')).toHaveCount(5);
    await expect.soft(builder.nodes('switch')).toHaveCount(3);
    await expect
      .soft(builder.canvasItem('host-a'))
      .toHaveAttribute('aria-label', /1 connection, on EXP$/);
    await expect
      .soft(builder.canvasItem('host-b'))
      .toHaveAttribute('aria-label', /2 connections, on EXP and MGMT$/);
    for (const [hostname, from] of [
      ['inc-host', child],
      ['svc-host', child],
      ['deep-host', nested],
    ]) {
      const item = builder.canvasItem(hostname);
      await expect.soft(item).toContainText(`Included from ${from}`);
      await expect
        .soft(item)
        .toHaveAttribute(
          'aria-label',
          new RegExp(`, from included topology ${from}, read only, `),
        );
    }
    for (const network of ['EXP', 'MGMT', 'SERVICES']) {
      await expect
        .soft(builder.canvasItem(network), network)
        .toHaveAttribute('aria-label', new RegExp(`network ${network}\\b`));
    }
    await builder.waitSaved();

    const stored = await builder.serverDraft(draft);
    expect.soft(stored.sourceToken).toBe(`Topology/${name}`);
    const doc = await builder.serverDocument(draft);
    expect
      .soft(doc.source)
      .toMatchObject({ kind: 'topology', name, includeTopologies: [child] });
    // Every include was read, so none is recorded as left out.
    expect.soft(doc.source).not.toHaveProperty('unresolvedIncludes');
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
            'Publishing does not copy the 3 devices from included topologies, their ' +
            '3 connections, or the 1 switch and 1 network only they use. ' +
            'The topology includes them by reference.',
        );
      await expect(publish.getByTestId('publish-name')).toHaveValue(name);
      await expect(publish.getByTestId('publish-submit')).toHaveText(
        'Update topology',
      );

      const published = page.waitForResponse(isPublishResponse);
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

  test('imports a draft from a stored experiment with VLAN aliases and included topologies', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const { topology, experiment, child, nested, scenario } =
      await storedExperiment(request, tracker, testInfo);
    await builder.open();

    const dialog = await openImport(page);
    await dialog.getByTestId('import-kind').selectOption('experiment');
    await expect
      .soft(dialog.getByTestId('import-name').locator('option').first())
      .toHaveText('Choose an experiment');
    await dialog.getByTestId('import-name').selectOption(experiment);
    // An experiment holds its nodes merged already: it has no options.
    for (const id of ['import-includes', 'import-copy', 'import-new-name']) {
      await expect.soft(dialog.getByTestId(id), id).toHaveCount(0);
    }

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await dialog.getByTestId('import-submit').click();
    // Nodes phenix merged in from included topologies are marked, not added.
    await expect
      .soft(dialog.getByTestId('import-warnings'))
      .toContainText(
        `The Builder marked 2 experiment nodes as included from included topologies ${child} (1 node) and ${nested} (1 node).`,
      );
    await continuePastWarnings(dialog);
    const draft = await (await created).json();

    await expect(page.getByTestId('builder-name')).toHaveText(experiment);
    await builder.expectSummary(
      '4 devices, 2 switches, 2 networks, 5 connections',
    );
    // The import placed the nodes, not a layout: the layout menu says
    // Default.
    await expect
      .soft(builder.toolbar('layout'))
      .toHaveAccessibleName('Default layout');
    await expect(
      page.locator(
        '#builder-canvas .vue-flow__node[aria-label^="Switch EXP,"]',
      ),
    ).toHaveAttribute('aria-label', /VLAN alias 101/);
    await expect(
      page.locator(
        '#builder-canvas .vue-flow__node[aria-label^="Switch MGMT,"]',
      ),
    ).toHaveAttribute('aria-label', /VLAN alias 102/);
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
    // The stored scenario is listed by name, and nothing of it copied.
    expect.soft(doc.scenarios).toEqual([scenario]);
    expect.soft(doc).not.toHaveProperty('scenario');
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

    await test.step("the GEXF download lists the stored scenario's apps on their hosts", async () => {
      const dialog = await builder.openDialog('download');
      const status = dialog.getByRole('status');
      const fileName = downloadFileName(experiment, 'gexf');
      const saved = `Saved ${fileName}: 4 devices, 2 networks and 5 connections.`;
      const isRead = (url) =>
        url.pathname.endsWith(`${API}/configs/Scenario/${scenario}`);
      // The document names the scenario only: each download reads it.
      const reads = [];
      const onResponse = (response) => {
        if (isRead(new URL(response.url()))) {
          reads.push(response.status());
        }
      };
      // Each device's app values, by hostname, in the file a download saves.
      async function downloadApps() {
        const file = await download(page, () =>
          dialog.getByTestId('download-gexf').click(),
        );
        expect.soft(file.name).toBe(fileName);

        return page.evaluate((text) => {
          const xml = new DOMParser().parseFromString(text, 'application/xml');
          const gexf = 'http://gexf.net/1.3';
          const values = (node) =>
            [...node.getElementsByTagNameNS(gexf, 'attvalue')].map((value) => [
              value.getAttribute('for'),
              value.getAttribute('value'),
            ]);

          return Object.fromEntries(
            [...xml.getElementsByTagNameNS(gexf, 'node')]
              .filter((node) =>
                values(node).some(
                  ([id, value]) => id === 'kind' && value === 'device',
                ),
              )
              .map((node) => [
                node.getAttribute('label'),
                Object.fromEntries(
                  values(node).filter(([id]) => /app/.test(id)),
                ),
              ]),
          );
        }, file.buffer.toString('utf8'));
      }
      page.on('response', onResponse);

      // A role that cannot read the scenario: the file leaves the apps out,
      // rather than saying the devices run none, and the status says why.
      await page.route(isRead, (route) =>
        route.fulfill({
          status: 403,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'forbidden' }),
        }),
      );
      expect.soft(await downloadApps()).toEqual({
        'host-a': {},
        'host-b': {},
        'inc-host': {},
        'deep-host': {},
      });
      await expect
        .soft(status)
        .toHaveText(
          `${saved} It lists no scenario apps: your role cannot read scenario ${scenario}.`,
        );
      await page.unroute(isRead);

      const traffic = {
        apps: '[e2e-traffic]',
        apps_text: 'e2e-traffic',
        app_count: '1',
      };
      expect.soft(await downloadApps()).toEqual({
        'host-a': traffic,
        'host-b': traffic,
        'inc-host': { app_count: '0' },
        'deep-host': { app_count: '0' },
      });
      // The status follows the read.
      await expect.soft(status).toHaveText(saved);
      page.off('response', onResponse);
      expect.soft(reads).toEqual([403, 200]);

      await page.keyboard.press('Escape');
      await expect(builder.dialog).toHaveCount(0);
    });

    await test.step("the Inspector shows the experiment's annotations and its scenario's apps", async () => {
      const inspector = builder.inspector;
      const annotations = inspector.getByTestId('inspector-annotations');
      await expect
        .soft(annotations.getByTestId('inspector-source'))
        .toContainText(`From Experiment ${experiment}, imported `);
      await expect
        .soft(annotations.locator('dt'))
        .toHaveText(['scenario', 'topology']);
      await expect
        .soft(annotations.locator('dd'))
        .toHaveText([scenario, topology]);

      // A scenario's apps come from its config, with app and host counts.
      const section = inspector.getByTestId('inspector-scenario');
      const named = `Scenario ${scenario} (2 apps, 2 hosts)`;
      await expect
        .soft(section.getByTestId('inspector-scenario-name'))
        .toHaveText(named);
      await expect
        .soft(section.getByRole('heading', { level: 4, name: named }))
        .toBeVisible();
      await expect.soft(section).not.toContainText('Apps and their hosts');
      const apps = section.getByTestId('inspector-scenario-apps');
      await expect
        .soft(apps.getByTestId('inspector-scenario-app-name'))
        .toHaveText(['e2e-traffic', 'e2e-monitor (no hosts)']);
      await expect
        .soft(apps.getByTestId('inspector-scenario-hosts'))
        .toHaveText(['host-a, host-b']);
      // The sub-heading and the counts read in both themes.
      for (const scheme of ['light', 'dark']) {
        await useColorScheme(page, scheme);
        await expectAccessible(page, {
          include: '[data-testid="inspector-scenario"]',
          soft: true,
        });
      }
      await useColorScheme(page, 'light');

      // Edit scenarios opens the Scenarios dialog. Clearing the box there
      // leaves focus on the same button, now Add scenario.
      const edit = section.getByRole('button', { name: 'Edit scenarios' });
      await edit.click();
      const dialog = page.getByRole('dialog', { name: 'Scenarios' });
      await expect(dialog).toBeVisible();
      const box = dialog.getByRole('checkbox', { name: scenario, exact: true });
      await expect.soft(box).toBeChecked();
      await box.uncheck();
      // The row stays, so focus stays on its box.
      await expect.soft(box).toBeFocused();
      await expect
        .soft(dialog.getByTestId('scenario-count'))
        .toHaveText('0 scenarios selected.');
      await dialog.getByTestId('scenario-submit').click();
      await expect(dialog).toBeHidden();
      await expect.soft(section).toContainText('No scenarios.');
      await expect
        .soft(section.getByRole('button', { name: 'Add scenario' }))
        .toBeFocused();
      await expect.soft(builder).toHaveAnnounced('Removed scenarios');
    });

    await test.step('devices of included topologies are shown read only', async () => {
      const row = builder.canvasItem('inc-host');
      await expect.soft(row).toContainText(`Included from ${child}`);
      await expect
        .soft(row)
        .toHaveAttribute(
          'aria-label',
          new RegExp(`, from included topology ${child}, read only, `),
        );
      await expect
        .soft(builder.node('deep-host', 'device'))
        .toContainText(`Included from ${nested}`);

      // Delete is refused with the reason, and the node keeps focus.
      await row.focus();
      await row.press('Delete');
      await expect
        .soft(page.getByTestId('canvas-notice'))
        .toContainText(
          `Included topology ${child} defines inc-host. This device is read only here.`,
        );
      await expect.soft(row).toBeFocused();
      await builder.expectSummary('4 devices');

      // The Inspector says where it is edited, and offers no edits.
      await builder.selectNode('inc-host');
      await expect
        .soft(builder.inspector.getByTestId('inspector-included-note'))
        .toContainText(`Change it in ${child}`);
      await expect
        .soft(builder.inspector.getByTestId('inspector-add-interface'))
        .toHaveCount(0);
    });
    expectNoFatal(issues);
  });

  test('Import refuses a config file it cannot convert and converts a YAML topology file', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    await builder.open();
    const creates = watchDraftCreates(page);

    await test.step('a Scenario file is refused', async () => {
      const scenario = [
        `apiVersion: phenix.sandia.gov/v2`,
        'kind: Scenario',
        'metadata:',
        `  name: ${uniqueName(testInfo, 'scenario')}`,
        'spec:',
        '  apps: []',
        '',
      ].join('\n');

      const { dialog, response } = await importFromFile(page, scenario);
      expect.soft(response.status(), 'import status').toBe(422);
      await expect
        .soft(dialog.getByTestId('import-error'))
        .toHaveText(
          'Could not import the diagram. ' +
            'The Builder cannot open Scenario configs.',
        );
      await expect(dialog).toBeVisible();
      // The refusal is about the chosen file, so it is reported there.
      const file = dialog.getByTestId('import-file');
      await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
      await expect.soft(file).toBeFocused();
      await expect
        .soft(file)
        .toHaveAccessibleDescription(
          /The Builder cannot open Scenario configs\.$/,
        );
      await expect.soft(dialog.getByTestId('import-submit')).toBeEnabled();
      expect.soft(creates, 'draft creates').toEqual([]);

      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(dialog).toBeHidden();
    });

    const name = uniqueName(testInfo, 'upload');
    // The legacy Builder's diagram is left out. A long value scrolls.
    const notes = Array.from(
      { length: 40 },
      (_, i) => `line ${i + 1} of the notes`,
    );
    const content = [
      `apiVersion: ${API_VERSION}`,
      'kind: Topology',
      'metadata:',
      `  name: ${name}`,
      '  annotations:',
      "    builder-xml: '<mxGraphModel><root/></mxGraphModel>'",
      '    owner: e2e',
      '    notes: |',
      ...notes.map((line) => `      ${line}`),
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
    const { response } = await importFromFile(page, content);
    expect(response.ok(), await response.text()).toBeTruthy();
    const draft = await (await created).json();

    await expect.soft(page.getByTestId('builder-name')).toHaveText(name);
    await expect
      .soft(builder.summary)
      .toContainText('2 devices, 1 switch, 1 network, 2 connections');
    await expect.soft(builder.canvasItem('web')).toBeVisible();
    await expect.soft(builder.canvasItem('db')).toBeVisible();
    await builder.waitSaved();

    await test.step("the Inspector lists the topology's annotations, but not the Builder's own", async () => {
      const annotations = builder.inspector.getByTestId(
        'inspector-annotations',
      );
      await expect
        .soft(
          annotations.getByRole('heading', { level: 3, name: 'Annotations' }),
        )
        .toBeVisible();
      await expect
        .soft(annotations.getByTestId('inspector-source'))
        .toContainText(`From Topology ${name}, imported `);
      await expect
        .soft(annotations.locator('dt'))
        .toHaveText(['notes', 'owner']);
      await expect.soft(annotations).not.toContainText('builder-xml');

      // The long value scrolls in its box, which Tab reaches, named by
      // its key. The short one takes no focus.
      const box = annotations.getByRole('region', { name: 'notes' });
      await expect.soft(box).toHaveAttribute('tabindex', '0');
      await expect
        .soft(annotations.locator('dd').nth(1).locator('[tabindex]'))
        .toHaveCount(0);
      await box.focus();
      await box.press('End');
      await expect
        .poll(() => box.evaluate((element) => element.scrollTop))
        .toBeGreaterThan(0);
      await expect.soft(box).toContainText('line 40 of the notes');
    });

    const stored = await builder.serverDraft(draft);
    expect.soft(stored.sourceToken).toBe(`uploaded/Topology/${name}`);
    const doc = await builder.serverDocument(draft);
    expect.soft(doc.source).toMatchObject({
      kind: 'topology',
      name,
      annotations: { owner: 'e2e', notes: `${notes.join('\n')}\n` },
    });
    expect.soft(Object.keys(doc.source.annotations)).toHaveLength(2);
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
  });

  test('Import names the config file it read in Details; a stored config names no file', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const name = uniqueName(testInfo, 'gen-file');
    const config = topologyConfig(name, sharedVlanNodes());
    const fileName = `${name}.topology.json`;
    await seedConfig(request, tracker, config);
    await builder.open();
    const row = builder.inspector.getByTestId('inspector-source-file');

    await test.step('a config file', async () => {
      const created = waitForApi(page, 'POST', '/builder/drafts');
      const { dialog } = await importFromFile(
        page,
        JSON.stringify(config),
        fileName,
      );
      await continuePastWarnings(dialog);
      const response = await created;
      const draft = await response.json();
      await expect(builder.canvas).toBeVisible();
      await builder.waitSaved();

      expect.soft(response.request().postDataJSON().sourceFile).toBe(fileName);
      expect.soft(draft.sourceFile).toBe(fileName);
      await expect.soft(row.locator('dd')).toHaveText(fileName);
      // The diagram was made here, now, from the config.
      await expectDetail(
        page,
        'created',
        draft.createdBy,
        draft.stamp.createdAt,
      );
    });

    await builder.backToDrafts();

    await test.step('a stored config', async () => {
      const dialog = await openImport(page);
      const choices = dialog.getByTestId('import-name');
      await expect(choices.locator('option', { hasText: name })).toHaveCount(1);
      await choices.selectOption(name);
      const created = waitForApi(page, 'POST', '/builder/drafts');
      await dialog.getByTestId('import-submit').click();
      await continuePastWarnings(dialog);
      const response = await created;
      const draft = await response.json();
      await expect(builder.canvas).toBeVisible();
      await builder.waitSaved();

      expect
        .soft(response.request().postDataJSON())
        .not.toHaveProperty('sourceFile');
      expect.soft(draft.sourceFile, 'sourceFile').toBeUndefined();
      await expect(
        builder.inspector.getByTestId('inspector-details'),
      ).toBeVisible();
      await expect.soft(row).toHaveCount(0);
    });

    expectNoFatal(issues);
  });

  test('shows import warnings before opening the draft', async ({
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

    // Cancel, left of Continue, closes the dialog as Escape does: no draft,
    // no word of an import, and focus back on Import.
    await test.step('Cancel leaves the warnings without creating a draft', async () => {
      const { dialog } = await importFromFile(page, content, 'topology.json');
      await expect(dialog.getByTestId('import-warnings')).toBeVisible();
      await expect
        .soft(dialog.locator('.builder-dialog__actions button'))
        .toHaveText(['Cancel', 'Continue to editor']);
      await dialog.getByTestId('import-cancel').click();
      await expect(dialog).toBeHidden();
      await expect.soft(page.getByTestId('drafts-import')).toBeFocused();
      await expect.soft(builder.landingHeading).toBeVisible();
      expect.soft(creates, 'draft creates after Cancel').toEqual([]);
      // Messages held while the dialog was open show once it closes, so
      // the log is checked again once the next import is announced.
      await expect.soft(builder).not.toHaveAnnounced(/imported/i);
    });

    const created = waitForApi(page, 'POST', '/builder/drafts');
    const { dialog, response } = await importFromFile(
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
    const shown = dialog.getByTestId('import-warnings');
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
      .soft(builder)
      .toHaveAnnounced('Imported the diagram, with 1 warning. Draft created.');
    expect
      .soft(
        (await builder.announced()).filter((text) => /imported/i.test(text)),
        'announcements of an import',
      )
      .toEqual([
        expect.stringContaining(
          'Imported the diagram, with 1 warning. Draft created.',
        ),
      ]);
    await expect(builder.canvas).toBeVisible();
    await expect.soft(builder.canvasItem('dup')).toBeVisible();
    await (await created).json();
    // Focus follows into the editor rather than falling to the page.
    expect
      .soft(await page.evaluate(() => document.activeElement !== document.body))
      .toBe(true);
    await builder.waitSaved();
    expectNoFatal(issues);
  });

  test('Import combines the included topologies into one new topology, which publishing creates', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const root = uniqueName(testInfo, 'comb');
    const child = uniqueName(testInfo, 'comb-inc');
    const combined = `${root}-combined`;
    await seedConfig(
      request,
      tracker,
      topologyConfig(child, [topologyNode('inc-host', [['eth0', 'EXP']])]),
    );
    const rootConfig = topologyConfig(root, [
      topologyNode('host-a', [['eth0', 'EXP']]),
    ]);
    rootConfig.spec.includeTopologies = [child];
    await seedConfig(request, tracker, rootConfig);
    const before = await builder.config('Topology', root);
    tracker.config('Topology', combined);

    await builder.open();
    const generates = [];
    page.on('request', (sent) => {
      if (
        sent.method() === 'POST' &&
        new URL(sent.url()).pathname.endsWith(`${API}/builder/generate`)
      ) {
        generates.push(sent.postDataJSON());
      }
    });
    const dialog = await openImport(page);
    await dialog.getByTestId('import-name').selectOption(root);
    const keep = dialog.getByLabel('Keep included nodes read only');
    const combine = dialog.getByLabel('Combine into one new topology');
    const newName = dialog.getByLabel('New topology name');
    const error = dialog.getByTestId('import-error');
    const submit = dialog.getByTestId('import-submit');

    await test.step('the arrow keys choose Combine, which asks for a name and proposes one', async () => {
      await expect.soft(keep).toBeChecked();
      await expect
        .soft(keep)
        .toHaveAccessibleDescription(
          `${root} includes 1 other topology. Their nodes are shown but cannot be changed here. Publishing keeps the includes.`,
        );
      await keep.focus();
      await page.keyboard.press('ArrowDown');
      await expect(combine).toBeChecked();
      // Focus stays in the group: what appears comes after it.
      await expect.soft(combine).toBeFocused();
      await expect
        .soft(combine)
        .toHaveAccessibleDescription(
          `${root} includes 1 other topology. Their nodes are copied into the diagram and can be changed. Publishing creates a new topology.`,
        );
      // Combine makes a new topology already, so the copy box goes.
      await expect.soft(dialog.getByTestId('import-copy')).toHaveCount(0);
      await expect(newName).toHaveValue(combined);
      // A name that keeps the naming rule shows no rule.
      await expect
        .soft(dialog.getByTestId('import-new-name-hint'))
        .toHaveCount(0);
      await expect.soft(newName).toHaveAccessibleDescription('');
      // Tab reaches the name next, then the buttons.
      await page.keyboard.press('Tab');
      await expect.soft(newName).toBeFocused();
    });

    await test.step('a name that cannot be used is refused on its field, before the server is asked', async () => {
      // While the name breaks the naming rule, the field says why, then the
      // rule, before anything is pressed.
      const hint = dialog.getByTestId('import-new-name-hint');
      const why =
        'This name is not valid: it contains a space and characters that a name cannot contain: "/". ' +
        'Names can use only letters, numbers, underscores (_), at signs (@), periods (.) and hyphens (-), with no spaces.';
      await newName.fill('a b/c');
      await expect.soft(hint).toHaveText(why);
      await expect.soft(newName).toHaveAccessibleDescription(why);

      for (const [typed, message] of [
        [root, `A topology named ${root} already exists. Enter another name.`],
        [
          'two words',
          'The topology name "two words" is not valid: it contains a space. Names can use only letters, numbers, underscores (_), at signs (@), periods (.) and hyphens (-), with no spaces. For example: two-words',
        ],
        ['', 'Enter a name for the topology.'],
      ]) {
        await newName.fill(typed);
        await newName.press('Enter');
        await expect.soft(error).toHaveText(message);
        await expect.soft(newName).toHaveAttribute('aria-invalid', 'true');
        await expect.soft(newName).toBeFocused();
        // The message describes the field. The rule shown under it while
        // the name breaks it is in the message, so it is not read twice.
        await expect.soft(newName).toHaveAccessibleDescription(message);
      }
      expect.soft(generates, 'imports asked of the server').toEqual([]);

      // Typing takes the error back, and a valid name takes the rule.
      await newName.fill(combined);
      await expect.soft(error).toHaveCount(0);
      await expect.soft(newName).not.toHaveAttribute('aria-invalid', 'true');
      await expect.soft(hint).toHaveCount(0);
    });

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await submit.click();
    const copied = `The Builder copied 1 node from included topology ${child} (1 node). They are ordinary nodes of this diagram now`;
    await expect(dialog.getByTestId('import-warnings')).toContainText(copied);
    expect(generates).toEqual([
      { source: `topology/${root}`, includes: 'combine', name: combined },
    ]);
    await continuePastWarnings(dialog);
    const draft = await (await created).json();
    await expect(dialog).toBeHidden();
    await expect(builder.canvas).toBeVisible();
    await expect
      .soft(builder)
      .toHaveAnnounced(
        `Combined topology ${root} and its included topologies as ${combined}, with 1 warning. Draft created.`,
      );

    await test.step('the draft is linked to no config, and every node is its own', async () => {
      await expect.soft(page.getByTestId('builder-name')).toHaveText(combined);
      await builder.expectSummary(
        '2 devices, 1 switch, 1 network, 2 connections',
      );
      await builder.waitSaved();
      const stored = await builder.serverDraft(draft);
      expect.soft(stored.sourceToken || '').toBe('');
      const doc = await builder.serverDocument(draft);
      expect.soft(doc.metadata.name).toBe(combined);
      expect.soft(doc.metadata.notes).toEqual([`Copied from ${root}`]);
      expect.soft(doc.source.kind).toBe('manual');
      for (const key of [
        'name',
        'digest',
        'includeTopologies',
        'unresolvedIncludes',
      ]) {
        expect.soft(doc.source, key).not.toHaveProperty(key);
      }
      expect
        .soft(doc.nodes.filter((node) => node.device?.includedFrom))
        .toEqual([]);

      // The node that was included is edited like any other.
      await expect
        .soft(builder.canvasItem('inc-host'))
        .not.toContainText('Included from');
      // Where that stood, it shows its node type, and the switch counts it
      // among the devices it connects.
      await expect
        .soft(builder.node('inc-host', 'device').getByTestId('node-type'))
        .toHaveText('VirtualMachine');
      await expect
        .soft(await switchWrapper(page, builder))
        .toHaveAccessibleDescription(
          /^2 connected devices: host-a 10\.0\.\d+\.\d+\/24, inc-host 10\.0\.\d+\.\d+\/24\./,
        );
      await builder.selectNode('inc-host');
      await expect
        .soft(builder.inspector.getByTestId('inspector-included-note'))
        .toHaveCount(0);
      const hostname = builder.inspector.getByRole('textbox', {
        name: 'Hostname',
        exact: true,
      });
      await expect(hostname).toBeEditable();
      await hostname.fill('inc-two');
      await builder.apply();
      await builder.persisted(
        draft,
        (document) =>
          document.nodes
            .filter((node) => node.kind === 'device')
            .map((node) => node.device.hostname)
            .sort(),
        ['host-a', 'inc-two'],
      );
      await builder.waitSaved();
    });

    await test.step('Publish creates the new topology, and cannot update the one it came from', async () => {
      const publish = await builder.openDialog('publish');
      const name = publish.getByTestId('publish-name');
      const hint = publish.locator('#publish-topology-action-hint');
      const go = publish.getByTestId('publish-submit');
      await expect(name).toHaveValue(combined);
      await expect.soft(hint).toHaveText('Publishing creates a new topology.');
      await expect.soft(go).toHaveText('Create topology');
      // Every node is the diagram's own, so all of them are published.
      await expect
        .soft(publish)
        .toContainText(
          '2 devices, 1 switch, 1 network and 2 connections are ready to publish.',
        );
      await expect.soft(publish).not.toContainText('by reference');

      await name.fill(root);
      await expect
        .soft(hint)
        .toHaveText(
          /^A topology with this name already exists\. This diagram cannot update it\. /,
        );
      await name.fill(combined);
      await expect(hint).toHaveText('Publishing creates a new topology.');

      const published = page.waitForResponse(isPublishResponse);
      await go.click();
      const response = await published;
      expect(response.status(), await response.text()).toBe(200);
      await expect
        .soft(publish.getByTestId('publish-summary'))
        .toHaveText('Published. Every stage succeeded.');

      const topology = await builder.config('Topology', combined);
      expect
        .soft(topology.spec.nodes.map((node) => node.general.hostname).sort())
        .toEqual(['host-a', 'inc-two']);
      expect.soft(topology.spec.includeTopologies || []).toEqual([]);
      // The topology it was combined from is as it was.
      expect.soft(await builder.config('Topology', root)).toEqual(before);
    });

    expectNoFatal(issues);
  });

  test('Import as a copy leaves the topology alone, and an open draft combines its included nodes into a new draft', async ({
    page,
    request,
    builder,
    tracker,
    issues,
  }, testInfo) => {
    const root = uniqueName(testInfo, 'copy');
    const child = uniqueName(testInfo, 'copy-inc');
    // Never stored: its nodes cannot be read, so every diagram keeps it as
    // an include.
    const missing = uniqueName(testInfo, 'copy-gone');
    const copyName = `${root}-copy`;
    const combinedName = `${copyName}-combined`;
    await seedConfig(
      request,
      tracker,
      topologyConfig(child, [topologyNode('inc-host', [['eth0', 'EXP']])]),
    );
    const rootConfig = topologyConfig(root, [
      topologyNode('host-a', [['eth0', 'EXP']]),
    ]);
    rootConfig.spec.includeTopologies = [child, missing];
    await seedConfig(request, tracker, rootConfig);
    const before = await builder.config('Topology', root);
    tracker.config('Topology', copyName);

    await builder.open();
    const dialog = await openImport(page);
    await dialog.getByTestId('import-name').selectOption(root);
    const copy = dialog.getByLabel('Create a new topology as a copy');
    const combine = dialog.getByLabel('Combine into one new topology');
    const newName = dialog.getByLabel('New topology name');

    await test.step('Space checks the copy box, which asks for a name that follows the choice', async () => {
      await expect
        .soft(dialog.getByTestId('import-includes'))
        .toContainText(`${root} includes 2 other topologies.`);
      await expect.soft(newName).toHaveCount(0);
      await copy.focus();
      await page.keyboard.press('Space');
      await expect(copy).toBeChecked();
      await expect.soft(copy).toBeFocused();
      await expect
        .soft(copy)
        .toHaveAccessibleDescription(
          `The draft is not linked to ${root}. Publishing creates a new topology and leaves ${root} as it is.`,
        );
      await expect(newName).toHaveValue(copyName);

      await combine.check();
      await expect.soft(copy).toHaveCount(0);
      await expect.soft(newName).toHaveValue(`${root}-combined`);
      await dialog.getByLabel('Keep included nodes read only').check();
      await expect.soft(copy).toBeChecked();
      await expect(newName).toHaveValue(copyName);
    });

    const generated = waitForApi(page, 'POST', '/builder/generate');
    const created = waitForApi(page, 'POST', '/builder/drafts');
    await dialog.getByTestId('import-submit').click();
    expect((await generated).request().postDataJSON()).toEqual({
      source: `topology/${root}`,
      includes: 'keep',
      copy: true,
      name: copyName,
    });
    const { warnings } = await (await generated).json();
    await expect(dialog.getByTestId('import-warnings')).toContainText(
      `The Builder added 1 node from included topology ${child} (1 node). The diagram shows them read only`,
    );
    await continuePastWarnings(dialog);
    const draft = await (await created).json();
    await expect(builder.canvas).toBeVisible();
    await expect
      .soft(builder)
      .toHaveAnnounced(
        `Imported a copy of topology ${root} as ${copyName}, with ${warnings.length} warnings. Draft created.`,
      );

    await test.step('the copy keeps its included nodes read only, names no config, and has a note that names its source', async () => {
      await expect.soft(page.getByTestId('builder-name')).toHaveText(copyName);
      await builder.waitSaved();
      expect
        .soft((await builder.serverDraft(draft)).sourceToken || '')
        .toBe('');
      const doc = await builder.serverDocument(draft);
      expect.soft(doc.metadata.name).toBe(copyName);
      expect.soft(doc.metadata.notes).toEqual([`Copied from ${root}`]);
      // With nothing selected, the Inspector lists the note.
      await expect
        .soft(
          builder.inspector
            .getByTestId('inspector-notes')
            .getByRole('textbox', { name: 'Note 1', exact: true }),
        )
        .toHaveValue(`Copied from ${root}`);
      expect.soft(doc.source).toMatchObject({
        kind: 'manual',
        includeTopologies: [child, missing],
        unresolvedIncludes: [missing],
      });
      expect.soft(doc.source).not.toHaveProperty('name');

      await expect
        .soft(builder.canvasItem('inc-host'))
        .toContainText('Included from');
      // On the canvas it says where it comes from in place of its type, and
      // the switch lists it with the diagram's own device.
      const included = builder.node('inc-host', 'device');
      await expect.soft(included).toContainText(`Included from ${child}`);
      await expect.soft(included.getByTestId('node-type')).toHaveCount(0);
      await expect
        .soft(builder.node('host-a', 'device').getByTestId('node-type'))
        .toHaveText('VirtualMachine');
      await expect
        .soft(await switchWrapper(page, builder))
        .toHaveAccessibleDescription(
          /^2 connected devices: host-a .*, inc-host /,
        );
      await builder.selectNode('inc-host');
      await expect
        .soft(builder.inspector.getByTestId('inspector-included-note'))
        .toHaveText(
          `Included topology ${child} defines this device. It is read only here. Change it in ${child}, and then import again. You can also combine the included nodes into a new draft to edit them here. You can still move it.`,
        );
    });

    await test.step('Publish creates the copy and refuses the name of the original', async () => {
      const publish = await builder.openDialog('publish');
      const name = publish.getByTestId('publish-name');
      const hint = publish.locator('#publish-topology-action-hint');
      await expect(name).toHaveValue(copyName);
      await expect.soft(hint).toHaveText('Publishing creates a new topology.');

      await name.fill(root);
      await expect
        .soft(hint)
        .toHaveText(
          /^A topology with this name already exists\. This diagram cannot update it\. /,
        );
      await name.fill(copyName);
      await expect(publish.getByTestId('publish-submit')).toHaveText(
        'Create topology',
      );

      const published = page.waitForResponse(isPublishResponse);
      await publish.getByTestId('publish-submit').click();
      const response = await published;
      expect(response.status(), await response.text()).toBe(200);

      const topology = await builder.config('Topology', copyName);
      expect
        .soft(topology.spec.nodes.map((node) => node.general.hostname))
        .toEqual(['host-a']);
      expect.soft(topology.spec.includeTopologies).toEqual([child, missing]);
      // Neither its content nor its time of change moved.
      expect.soft(await builder.config('Topology', root)).toEqual(before);
      await publish.getByRole('button', { name: 'Close dialog' }).click();
      await expect(publish).toBeHidden();
    });

    const stillIncludes = `It still includes ${missing}, whose nodes are not in the diagram.`;
    const combinedSaid = `Combined 1 included node into new draft ${combinedName}. Draft ${copyName} is unchanged. ${stillIncludes}`;

    const first =
      await test.step("the Inspector's button makes a new draft whose nodes are all its own", async () => {
        await builder.selectNode('inc-host');
        const button = builder.inspector.getByTestId('inspector-combine');
        await expect(button).toHaveText('Combine into a new draft');
        await expect
          .soft(button)
          .toHaveAccessibleDescription(
            /^Included topology \S+ defines this device/,
          );

        const made = waitForApi(page, 'POST', '/builder/drafts');
        await button.focus();
        await page.keyboard.press('Enter');
        const body = await (await made).json();
        expect.soft(body.id).not.toBe(draft.id);
        await expect(page.getByTestId('builder-name')).toHaveText(combinedName);
        await expect.soft(builder).toHaveAnnounced(combinedSaid);
        // The button went with the note: focus is on the editor's heading.
        await expect
          .soft(page.getByRole('heading', { level: 1 }))
          .toBeFocused();
        await builder.waitSaved();

        const doc = await builder.serverDocument(body);
        expect.soft(doc.source).toEqual({
          kind: 'manual',
          importedAt: expect.any(String),
          includeTopologies: [missing],
        });
        expect
          .soft(doc.nodes.filter((node) => node.device?.includedFrom))
          .toEqual([]);
        expect
          .soft((await builder.serverDraft(body)).sourceToken || '')
          .toBe('');
        await expect
          .soft(builder.canvasItem('inc-host'))
          .not.toContainText('Included from');
        await expect
          .soft(builder.node('inc-host', 'device').getByTestId('node-type'))
          .toHaveText('VirtualMachine');
        await builder.selectNode('inc-host');
        await expect
          .soft(builder.inspector.getByTestId('inspector-included-note'))
          .toHaveCount(0);
        await expect
          .soft(builder.inspector.getByTestId('inspector-combine'))
          .toHaveCount(0);

        // Publishing writes both nodes, and still names the include whose
        // nodes were never in the diagram.
        const publish = await builder.openDialog('publish');
        await expect(publish.getByTestId('publish-name')).toHaveValue(
          combinedName,
        );
        await expect
          .soft(publish)
          .toContainText(
            `2 devices, 1 switch, 1 network and 2 connections are ready to publish. The published topology also includes ${missing} by reference.`,
          );
        await publish.getByRole('button', { name: 'Close dialog' }).click();
        await expect(publish).toBeHidden();

        return body;
      });

    await test.step('the draft it was made from still has its read-only nodes', async () => {
      const doc = await builder.serverDocument(draft);
      expect
        .soft(
          doc.nodes
            .filter((node) => node.device?.includedFrom)
            .map((node) => [node.device.hostname, node.device.includedFrom]),
        )
        .toEqual([['inc-host', child]]);
      expect.soft(doc.source.includeTopologies).toEqual([child, missing]);
    });

    await test.step('the command palette offers it only while there are included nodes', async () => {
      const field = page.getByRole('combobox', { name: 'Search commands' });
      const offered = page.getByTestId('commands-dialog').getByRole('option', {
        name: /Combine included nodes into a new draft/,
      });

      // The combined draft has none.
      await page.getByTestId('editor-commands').click();
      await field.fill('combine included');
      await expect(field).toHaveValue('combine included');
      await expect.soft(offered).toHaveCount(0);
      await page.keyboard.press('Escape');

      await builder.backToDrafts();
      await page.getByTestId(`draft-open-${draft.id}`).click();
      await expect(page.getByTestId('builder-name')).toHaveText(copyName);
      await builder.waitSaved();

      await page.getByTestId('editor-commands').click();
      await field.fill('unlock');
      await expect(offered).toHaveCount(1);
      await expect
        .soft(offered)
        .toContainText(
          '1 included node becomes editable in a copy of this diagram',
        );
      const made = waitForApi(page, 'POST', '/builder/drafts');
      await page.keyboard.press('Enter');
      const second = await (await made).json();
      expect.soft([draft.id, first.id]).not.toContain(second.id);
      // The first combined draft has the name, so this one takes the next:
      // two drafts of one name would propose the same topology at Publish.
      await expect(page.getByTestId('builder-name')).toHaveText(
        `${combinedName}-2`,
      );
      await builder.waitSaved();
      expect
        .soft((await builder.serverDraft(second)).title)
        .toBe(`${combinedName}-2`);
      expect
        .soft(
          (await builder.serverDocument(second)).nodes.filter(
            (node) => node.device?.includedFrom,
          ),
        )
        .toEqual([]);
      // Focus is in the editor, not on the page.
      expect
        .soft(
          await page.evaluate(() => document.activeElement !== document.body),
        )
        .toBe(true);
    });

    expectNoFatal(issues);
  });

  test('config files are converted verbatim, without environment expansion', async ({
    page,
    builder,
  }, testInfo) => {
    await builder.open();
    const name = uniqueName(testInfo, 'env');
    // A ${NAME:default} placeholder shows an expansion even when the server
    // has no such variable, and reveals no real one.
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
    const { response } = await importFromFile(page, content, 'topology.json');
    const draft = await (await created).json();
    expect(descriptionOf((await response.json()).document)).toBe(placeholder);
    await builder.waitSaved();
    expect(descriptionOf(await builder.serverDocument(draft))).toBe(
      placeholder,
    );
  });
});

// --- drafts landing ----------------------------------------------------------

test.describe('drafts landing', () => {
  // The landing reads its lists again when the page comes back into view,
  // quietly and once for the pair of events.
  test('the lists refresh when the page comes back into view, and Delete asks, then removes a draft', async ({
    page,
    request,
    builder,
    issues,
  }, testInfo) => {
    const initial = waitForApi(page, 'GET', '/builder/drafts');
    await builder.open();
    await initial;

    const title = uniqueName(testInfo, 'refresh');
    const draft = await builder.seedDraft(blankDocument(title));
    const path = draftPath(draft);
    const open = page.getByTestId(`draft-open-${draft.id}`);
    const remove = page.getByRole('button', { name: `Delete ${title}` });
    // Named with its time, as on its card: most drafts share a title.
    const confirm = page.getByRole('alertdialog', {
      name: new RegExp(`^Delete draft ${title}, updated .+\\?$`),
    });
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
      await expect.soft(page.getByTestId('drafts-tab-drafts')).toBeFocused();
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

    // Before the next step makes the card's ETag stale, so no list read
    // meanwhile changes what its Delete sends.
    await test.step('Delete asks first, and Escape or a click outside keeps the draft', async () => {
      let deleteSent = false;
      page.on('request', (sent) => {
        if (sent.method() === 'DELETE' && sent.url().includes(draft.id)) {
          deleteSent = true;
        }
      });

      // An alert dialog that names the draft, with focus on the safe choice.
      await remove.click();
      await expect(confirm).toBeVisible();
      expect(deleteSent, 'DELETE sent before any confirmation').toBe(false);
      await expect
        .soft(confirm)
        .toHaveAccessibleDescription(/You cannot undo this/);
      await expect(
        confirm.getByRole('button', { name: 'Cancel' }),
      ).toBeFocused();
      await expectAccessible(page, {
        include: '[data-testid="builder-confirm"]',
        soft: true,
      });

      // Dismissing it keeps the draft and returns focus to Delete.
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
      expect((await request.get(path)).ok(), 'server copy').toBeTruthy();
    });

    await test.step('Delete removes it from Drafts and the server, although it changed since the list was read', async () => {
      // The card's ETag is then older than the server's, as it is right
      // after the Drafts button until the list is read again.
      const current = await request.get(path);
      expect(current.ok(), await current.text()).toBeTruthy();
      const { document } = await current.json();
      const changed = await request.post(`${path}/snapshots`, {
        headers: { 'If-Match': current.headers().etag },
        data: {
          summary: 'Changed elsewhere',
          document: {
            ...(typeof document === 'string' ? JSON.parse(document) : document),
            viewport: { x: 40, y: 40, zoom: 1 },
          },
        },
      });
      expect(changed.ok(), await changed.text()).toBeTruthy();

      const deletes = [];
      page.on('response', (response) => {
        if (
          response.request().method() === 'DELETE' &&
          new URL(response.url()).pathname.endsWith(path)
        ) {
          deletes.push(response.status());
        }
      });
      await remove.click();
      await confirm.getByRole('button', { name: 'Delete draft' }).click();

      await expect.soft(draftCard(page, draft.id)).toHaveCount(0);
      // Refused for the old ETag, the draft is deleted with its current one.
      expect.soft(deletes, 'DELETE statuses').toEqual([412, 204]);
      await expect
        .soft(builder)
        .toHaveAnnounced(new RegExp(`Deleted draft ${title}, updated .+\\.`));
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
                createdBy: listed.createdBy,
                title: listed.title,
                updated: listed.updated,
                etag: listed.etag,
              },
            ]
          : [];

        return route.fulfill({ response, json: body });
      });
      await showPageAgain(page);

      const card = page.getByTestId('drafts-list-drafts').locator('li', {
        has: page.getByTestId(`draft-damaged-${unreadable.id}`),
      });
      await expect(card).toContainText(
        'You cannot open this draft, because the Builder cannot read it.',
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
});
