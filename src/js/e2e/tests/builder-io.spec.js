// Builder import, upload, download and drafts-landing flows.
//
// Every draft the page creates is removed by the `tracker` fixture. Drafts,
// topologies and experiments this spec creates through the API are registered
// with the tracker too (seedDraft, publishTopology and createExperiment).

const fs = require('fs');

const {
  API,
  DOCUMENT_TIME,
  SCHEMA_URI,
  backdropPoint,
  blankDocument,
  draftPath,
  expect,
  expectAccessible,
  expectDetail,
  expectNoFatal,
  knownDefect,
  publishTopology,
  seedConfig,
  test,
  uniqueName,
  visit,
  waitForApi,
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

// A phenix v1 topology node. `interfaces` is a list of [name, vlan] pairs.
// Its addresses are the hostname's own, as publishing refuses two
// interfaces with one address.
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
// generated from a config file the way Import builds it. Returns the
// topology name, the source draft and the published document's id. The
// draft is deleted once it has published, so the diagram opens in a new
// draft, as it does for a user who did not publish it.
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

// Creates an experiment from a stored topology, and a stored scenario when
// one is named, or skips the test when this phenix server cannot create
// experiments.
async function createExperiment(request, tracker, name, topology, scenario) {
  const created = await request.post(`${API}/experiments`, {
    data: { name, topology, ...(scenario ? { scenario } : {}) },
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
  // A new node is selected, and Shift with an arrow key moves it 10px.
  const device = flowNode(builder, 'device');
  await expect(device).toBeVisible();
  await device.focus();
  for (let step = 0; step < 16; step += 1) {
    await builder.page.keyboard.press('Shift+ArrowDown');
  }
  await builder.connect();
  await builder.expectSummary('1 device, 1 switch, 1 network, 1 connection');
  await expect(builder.page.getByTestId('builder-name')).toHaveText(title);
  // An edit is written to IndexedDB before the upload starts, so for a
  // moment after an edit the save state still reads "All changes saved"
  // from the previous save: the server copy is waited for first.
  await builder.persisted(
    draft,
    (doc) => {
      const [sw, node] = ['switch', 'device'].map((kind) =>
        doc.nodes.find((item) => item.kind === kind),
      );

      return (
        doc.name === title &&
        doc.nodes.length === 2 &&
        doc.edges.length === 1 &&
        node.position.y - sw.position.y === 160
      );
    },
    true,
    { timeout: 5000, message: 'the latest edit reaches the server' },
  );
  await builder.waitSaved();

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

// Soft-checks a downloaded image, as the browser draws it, against `diagram`
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

// Soft-checks the markup of a downloaded SVG against `diagram` (from
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

function downloadFileName(title, extension) {
  const base = title
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return `${base || 'topology'}.${extension}`;
}

// The landing's Import button opens the Import dialog, and its Upload
// button the Upload dialog. Each is found by its title, so a test id that
// opened the other one fails here.
async function openImport(page) {
  await page.getByTestId('drafts-import').click();
  const dialog = page.getByRole('dialog', {
    name: 'Import topology or experiment',
  });
  await expect(dialog).toBeVisible();

  return dialog;
}

async function openUpload(page) {
  await page.getByTestId('drafts-upload').click();
  const dialog = page.getByRole('dialog', { name: 'Upload diagram' });
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

// Import stops on its warnings, and creates the draft only when the user
// continues past them. Continues when there are warnings; otherwise the
// dialog has closed by itself.
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
    .getByTestId('drafts-list-mine')
    .locator('li', { has: page.getByTestId(`draft-open-${id}`) });
}

// The page-level error: an alert with its own Dismiss button.
function errorBanner(page) {
  return page.getByTestId('builder-error').getByRole('alert');
}

// --- download and upload -----------------------------------------------------

test.describe('download and upload', () => {
  test(
    'downloads JSON, YAML, Topology YAML, GEXF, PNG and SVG, and uploads the Builder files as new drafts',
    {
      tag: '@cross-browser',
    },
    async ({ page, request, builder, issues }, testInfo) => {
      await builder.open();
      const title = uniqueName(testInfo, 'download');
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
      // images are made, the canvas is zoomed in, which pans it too, and has
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

      const dialog = await builder.openDialog('download');
      const status = dialog.getByRole('status');
      const downloadError = dialog.getByTestId('download-error');
      // The status region is there, empty, before the first download, so
      // screen readers announce the first message too.
      await expect.soft(status).toHaveText('');

      await test.step('the formats are in two rows, and Gephi links to its project', async () => {
        const boxes = {};
        for (const format of [
          'json',
          'yaml',
          'topology-yaml',
          'png',
          'svg',
          'gexf',
        ]) {
          boxes[format] = await dialog
            .getByTestId(`download-${format}`)
            .boundingBox();
        }
        // The documents and the config, then the pictures and the graph:
        // each row's buttons share a top, in order, and the second row
        // starts under the first.
        for (const row of [
          ['json', 'yaml', 'topology-yaml'],
          ['png', 'svg', 'gexf'],
        ]) {
          expect
            .soft(
              row.map((format) =>
                Math.round(boxes[format].y - boxes[row[0]].y),
              ),
              `tops of ${row}`,
            )
            .toEqual([0, 0, 0]);
          expect
            .soft(
              row.map((format) => boxes[format].x),
              `${row} left to right`,
            )
            .toEqual(
              row.map((format) => boxes[format].x).sort((a, b) => a - b),
            );
        }
        expect
          .soft(boxes.png.y, 'PNG is under Builder JSON')
          .toBeGreaterThanOrEqual(boxes.json.y + boxes.json.height);
        expect.soft(boxes.png.x, 'the rows start together').toBe(boxes.json.x);

        const link = dialog.getByTestId('download-gephi-link');
        await expect
          .soft(link)
          .toHaveAccessibleName('Gephi (opens in a new tab)');
        await expect.soft(link).toHaveAttribute('href', 'https://gephi.org/');
        await expect.soft(link).toHaveAttribute('target', '_blank');
        await expect.soft(link).toHaveAttribute('rel', /\bnoopener\b/);
        await expect.soft(link).toHaveAttribute('rel', /\bnoreferrer\b/);
        await expect.soft(link).toHaveCSS('text-decoration-line', 'underline');
        // A Tab stop after the format buttons, before Close.
        await dialog.getByTestId('download-gexf').focus();
        await page.keyboard.press('Tab');
        await expect.soft(link).toBeFocused();
        await page.keyboard.press('Tab');
        await expect
          .soft(dialog.getByRole('button', { name: 'Close', exact: true }))
          .toBeFocused();
      });

      // The file name, the status line and the absence of an error, after
      // each download.
      async function expectSaved(file, extension) {
        const fileName = downloadFileName(title, extension);
        expect.soft(file.name, `${extension} file name`).toBe(fileName);
        await expect.soft(status).toHaveText(`Saved ${fileName}.`);
        await expect
          .soft(downloadError, `${extension} download error`)
          .toHaveCount(0);
      }

      const json =
        await test.step('the JSON download is the saved document', async () => {
          const file = await download(page, () =>
            dialog.getByTestId('download-json').click(),
          );
          await expectSaved(file, 'json');

          const downloaded = JSON.parse(file.buffer.toString('utf8'));
          expect.soft(downloaded.$schema).toBe(SCHEMA_URI);
          expect.soft(downloaded.revision).toBe(1);
          expect.soft(downloaded.name).toBe(title);
          expect
            .soft(downloaded.nodes.map((node) => node.kind).sort())
            .toEqual(['device', 'switch']);
          expect
            .soft(downloaded.networks.map((network) => network.name))
            .toEqual(['EXP']);
          expect.soft(downloaded.edges).toHaveLength(1);
          expect.soft(downloaded.id).toBe(saved.id);
          expect.soft(downloaded.nodes).toEqual(saved.nodes);
          expect.soft(downloaded.edges).toEqual(saved.edges);

          return { file, downloaded };
        });

      const yaml =
        await test.step('the YAML download has the document keys', async () => {
          const file = await download(page, () =>
            dialog.getByTestId('download-yaml').click(),
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
        // The image is drawn from a copy of the canvas, removed after; the
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
          // The server drops empty optional fields, so compare the stored copies.
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
      // handler runs in the click task; a later task sees its DOM update.
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
        /^A Topology config is not a Builder document\. Use Import on the drafts page\b/,
      ],
      [
        'an Experiment config',
        JSON.stringify({
          apiVersion: API_VERSION,
          kind: 'Experiment',
          metadata: { name },
          spec: {},
        }),
        /^An Experiment config is not a Builder document\. Use Import on the drafts page\b/,
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
        // An accepted input closes the dialog and creates a draft; stop here
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
      const draft = await (await created).json();

      await expect(dialog).toBeHidden();
      await expect(builder.canvas).toBeVisible();
      await expect.soft(page.getByTestId('builder-name')).toHaveText(title);
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

  test('an uploaded file keeps the author it names, shows the uploader as its last editor, and is named in Details', async ({
    page,
    builder,
    issues,
  }, testInfo) => {
    const title = uniqueName(testInfo, 'claimed');
    const fileName = `${title}.builder.json`;
    const claimed = {
      author: 'alice',
      createdAt: '2020-01-02T03:04:05Z',
      updatedBy: 'alice',
      updatedAt: '2020-02-03T04:05:06Z',
    };

    await builder.open();
    const dialog = await openUpload(page);
    await dialog.getByTestId('upload-file').setInputFiles({
      name: fileName,
      mimeType: 'application/json',
      buffer: Buffer.from(
        JSON.stringify({ ...blankDocument(title), ...claimed }),
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

    await test.step('the author the file names is kept, and the uploader edited it last', async () => {
      expect(draft.stamp).toEqual({
        author: claimed.author,
        createdAt: claimed.createdAt,
        updatedBy: draft.owner,
        updatedAt: expect.stringMatching(DOCUMENT_TIME),
      });
      expect.soft(draft.stamp.updatedAt).not.toBe(claimed.updatedAt);
      await expectDetail(page, 'created', claimed.author, claimed.createdAt);
      await expectDetail(page, 'edited', draft.owner, draft.stamp.updatedAt);
    });

    await test.step('a save keeps both, and the file name', async () => {
      const saved = builder.nextSnapshot();
      await builder.rename(`${title} renamed`);
      const { stamp, sourceFile } = await (await saved).json();
      expect.soft(stamp).toMatchObject({
        author: claimed.author,
        createdAt: claimed.createdAt,
        updatedBy: draft.owner,
      });
      expect.soft(sourceFile).toBe(fileName);
      await expectDetail(page, 'created', claimed.author, claimed.createdAt);
      await expect
        .soft(builder.inspector.getByTestId('inspector-source-file'))
        .toContainText(fileName);
      await builder.waitSaved();
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
        .toHaveText(`Topology ${name} does not exist, or you may not read it.`);
      await expect.soft(builder.canvas).toHaveCount(0);
      await expect.soft(page.getByRole('dialog')).toHaveCount(0);
      await expect.soft(page.getByTestId('drafts-blank')).toBeEnabled();
      expect.soft(creates, 'draft creates').toEqual([]);
    });

    // Each path opens a diagram whose publishing draft is gone and which
    // has not been opened before, so each makes a new draft. Opening one
    // again reopens its draft (see the ?topology= link test in
    // builder-persistence.spec.js).
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

    // Other specs may leave experiments on a shared server; answer the
    // sources request with the real catalog minus its experiments so the
    // empty state is deterministic.
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
      `Added 3 nodes from included topologies ${child} (2 nodes) and ` +
      `${nested} (1 node). They are shown read only`;
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

  test('imports a draft from a stored experiment with VLAN aliases and included topologies', async ({
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
    // User apps this server does not have, which phenix skips when it
    // creates the experiment: one on two hosts, one on none. phenix uses a
    // scenario only with the topology it names.
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
    // The nodes phenix merged in from the included topologies are marked,
    // not added again. Experiment fields the diagram does not carry come
    // back as a warning too.
    await expect
      .soft(dialog.getByTestId('import-warnings'))
      .toContainText(
        `Marked 2 experiment nodes as coming from included topologies ${child} (1 node) and ${nested} (1 node).`,
      );
    await continuePastWarnings(dialog);
    const draft = await (await created).json();

    await expect(page.getByTestId('builder-name')).toHaveText(experiment);
    await builder.expectSummary(
      '4 devices, 2 switches, 2 networks, 5 connections',
    );
    // phenix stores "external": null on every VM of an experiment, which
    // once drew them all as external (hardware) devices.
    await expect
      .soft(builder.nodes('device').locator('.builder-icon--external'))
      .toHaveCount(0);
    // The import placed the nodes, not a layout: the layout menu says
    // Default.
    await expect
      .soft(builder.toolbar('layout'))
      .toHaveAccessibleName('Default layout');
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
    // The stored scenario is referenced, not copied.
    expect.soft(doc.scenario).toMatchObject({ kind: 'stored', name: scenario });
    expect.soft(doc.scenario.content).toBeUndefined();
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
      // The reference carries no content: each download reads the scenario.
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

      // A stored scenario's apps are read from its config.
      const section = inspector.getByTestId('inspector-scenario');
      await expect
        .soft(section.getByTestId('inspector-scenario-name'))
        .toHaveText(`Stored scenario ${scenario}`);
      const apps = section.getByTestId('inspector-scenario-apps');
      await expect
        .soft(apps.locator('dt'))
        .toHaveText(['e2e-traffic', 'e2e-monitor']);
      await expect
        .soft(apps.locator('dd'))
        .toHaveText(['host-a, host-b', 'No hosts']);

      // Edit scenario opens the toolbar's Scenario dialog. Removing the
      // scenario there leaves focus on the same button, now Add scenario.
      const edit = section.getByRole('button', { name: 'Edit scenario' });
      await edit.click();
      const dialog = page.getByRole('dialog', { name: 'Scenario' });
      await expect(dialog).toBeVisible();
      await expect.soft(dialog.getByLabel('Stored scenario')).toBeChecked();
      await expect
        .soft(dialog.getByTestId('scenario-name'))
        .toHaveValue(scenario);
      await dialog.getByLabel('No scenario').check();
      await dialog.getByTestId('scenario-submit').click();
      await expect(dialog).toBeHidden();
      await expect.soft(section).toContainText('No scenario.');
      await expect
        .soft(section.getByRole('button', { name: 'Add scenario' }))
        .toBeFocused();
      await expect.soft(builder).toHaveAnnounced('Removed scenario');
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

  test(
    'Import refuses a config file it cannot convert and converts a YAML topology file',
    {
      tag: '@cross-browser',
    },
    async ({ page, builder, issues }, testInfo) => {
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
              'Scenario configs cannot be opened in the builder.',
          );
        await expect(dialog).toBeVisible();
        // The refusal is about the chosen file, so it is reported there.
        const file = dialog.getByTestId('import-file');
        await expect.soft(file).toHaveAttribute('aria-invalid', 'true');
        await expect.soft(file).toBeFocused();
        await expect
          .soft(file)
          .toHaveAccessibleDescription(/cannot be opened in the builder\.$/);
        await expect.soft(dialog.getByTestId('import-submit')).toBeEnabled();
        expect.soft(creates, 'draft creates').toEqual([]);

        await dialog.getByRole('button', { name: 'Cancel' }).click();
        await expect(dialog).toBeHidden();
      });

      const name = uniqueName(testInfo, 'upload');
      // The legacy Builder's diagram is left out; a long value scrolls.
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
      await expect.soft(builder.outlineItem('web')).toBeVisible();
      await expect.soft(builder.outlineItem('db')).toBeVisible();
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
        // its key; the short one takes no focus.
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
    },
  );

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
      await expectDetail(page, 'created', draft.owner, draft.stamp.createdAt);
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
      .toHaveAnnounced(
        'The diagram was imported with 1 warning. Draft created.',
      );
    expect
      .soft(
        (await builder.announced()).filter((text) => /imported/i.test(text)),
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
      await expect
        .soft(newName)
        .toHaveAccessibleDescription(
          'Names can use only letters, numbers, underscores (_), at signs (@), periods (.) and hyphens (-), with no spaces.',
        );
      // Tab reaches the name next, then the buttons.
      await page.keyboard.press('Tab');
      await expect.soft(newName).toBeFocused();
    });

    await test.step('a name that cannot be used is refused on its field, before the server is asked', async () => {
      for (const [typed, message] of [
        [root, `A topology named ${root} already exists. Enter another name.`],
        [
          'two words',
          'The topology name "two words" is not allowed. Names can use only letters, numbers, underscores (_), at signs (@), periods (.) and hyphens (-), with no spaces. For example: two-words',
        ],
        ['', 'Enter a name for the topology.'],
      ]) {
        await newName.fill(typed);
        await newName.press('Enter');
        await expect.soft(error).toHaveText(message);
        await expect.soft(newName).toHaveAttribute('aria-invalid', 'true');
        await expect.soft(newName).toBeFocused();
        await expect
          .soft(newName)
          .toHaveAccessibleDescription(new RegExp(' Enter .*name|not allowed'));
      }
      expect.soft(generates, 'imports asked of the server').toEqual([]);

      // Typing takes the error back.
      await newName.fill(combined);
      await expect.soft(error).toHaveCount(0);
      await expect.soft(newName).not.toHaveAttribute('aria-invalid', 'true');
    });

    const created = waitForApi(page, 'POST', '/builder/drafts');
    await submit.click();
    const copied = `Copied 1 node from included topology ${child} (1 node). They are ordinary nodes of this diagram now`;
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
      expect.soft(doc.name).toBe(combined);
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
      const row = builder.outlineItem('inc-host');
      await expect.soft(row).not.toContainText('included');
      await expect
        .soft(builder.node('inc-host', 'device'))
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
      await builder.selectInOutline('inc-host');
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
      await expect.soft(hint).toHaveText('A new topology will be created.');
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
          /^A topology with this name already exists, and this diagram cannot update it: /,
        );
      await name.fill(combined);
      await expect(hint).toHaveText('A new topology will be created.');

      const published = page.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname.endsWith('/publish'),
      );
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
      `Added 1 node from included topology ${child} (1 node). They are shown read only`,
    );
    await continuePastWarnings(dialog);
    const draft = await (await created).json();
    await expect(builder.canvas).toBeVisible();
    await expect
      .soft(builder)
      .toHaveAnnounced(
        `Imported a copy of topology ${root} as ${copyName}, with ${warnings.length} warnings. Draft created.`,
      );

    await test.step('the copy keeps its included nodes read only, and names no config', async () => {
      await expect.soft(page.getByTestId('builder-name')).toHaveText(copyName);
      await builder.waitSaved();
      expect
        .soft((await builder.serverDraft(draft)).sourceToken || '')
        .toBe('');
      const doc = await builder.serverDocument(draft);
      expect.soft(doc.name).toBe(copyName);
      expect.soft(doc.source).toMatchObject({
        kind: 'manual',
        includeTopologies: [child, missing],
        unresolvedIncludes: [missing],
      });
      expect.soft(doc.source).not.toHaveProperty('name');

      await expect
        .soft(builder.outlineItem('inc-host'))
        .toContainText('included');
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
      await builder.selectInOutline('inc-host');
      await expect
        .soft(builder.inspector.getByTestId('inspector-included-note'))
        .toHaveText(
          `Defined by included topology ${child}, so it is read only here. Change it in ${child} and import again, or combine the included nodes into a new draft to edit them here. It can still be moved.`,
        );
    });

    await test.step('Publish creates the copy and refuses the name of the original', async () => {
      const publish = await builder.openDialog('publish');
      const name = publish.getByTestId('publish-name');
      const hint = publish.locator('#publish-topology-action-hint');
      await expect(name).toHaveValue(copyName);
      await expect.soft(hint).toHaveText('A new topology will be created.');

      await name.fill(root);
      await expect
        .soft(hint)
        .toHaveText(
          /^A topology with this name already exists, and this diagram cannot update it: /,
        );
      await name.fill(copyName);
      await expect(publish.getByTestId('publish-submit')).toHaveText(
        'Create topology',
      );

      const published = page.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname.endsWith('/publish'),
      );
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
        await builder.selectInOutline('inc-host');
        const button = builder.inspector.getByTestId('inspector-combine');
        await expect(button).toHaveText('Combine into a new draft');
        await expect
          .soft(button)
          .toHaveAccessibleDescription(/^Defined by included topology /);

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
          .soft(builder.outlineItem('inc-host'))
          .not.toContainText('included');
        await expect
          .soft(builder.node('inc-host', 'device').getByTestId('node-type'))
          .toHaveText('VirtualMachine');
        await builder.selectInOutline('inc-host');
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

  test(
    'config files are converted verbatim, without environment expansion',
    {
      tag: '@known-defect',
    },
    async ({ page, builder }, testInfo) => {
      // Config files are limited to users who may create configs, who can reach
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
      const { response } = await importFromFile(page, content, 'topology.json');
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

      // The four tabs every user has. A fifth, of other users' drafts, shows
      // only while there are some, which a server without sign-in never has.
      const ids = ['mine', 'shared', 'published', 'templates'];
      const tabs = ids.map((id) => page.getByTestId(`drafts-tab-${id}`));
      const panels = ids.map((id) => page.locator(`#panel-${id}`));

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
      await expectActive(3);
      await page.keyboard.press('ArrowRight');
      await expectActive(0);
      await page.keyboard.press('ArrowLeft');
      await expectActive(3);
      await page.keyboard.press('ArrowLeft');
      await expectActive(2);

      await tabs[0].click();
      await expectActive(0);
    },
  );

  // There is no Refresh button: the landing reads its lists again when the
  // page comes back into view, quietly and once for the pair of events.
  test('the lists refresh when the page comes back into view, and Delete asks, then removes a draft', async ({
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
        .toHaveAccessibleDescription(/cannot be undone/);
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

    await test.step('Delete removes it from My Drafts and the server, although it changed since the list was read', async () => {
      // The card's ETag is then older than the server's, as it is right
      // after Back to drafts until the list is read again.
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
});
