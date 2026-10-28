// @vitest-environment happy-dom
// The Download dialog while a format is made: a ring in place of the
// button's icon, the status, one format at a time, and no second start.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

const exporters = vi.hoisted(() => ({
  exportImage: vi.fn(),
  saveText: vi.fn(),
}));

vi.mock('@/builder/exporters.js', async (importOriginal) => ({
  ...(await importOriginal()),
  ...exporters,
}));

import DownloadDialog from '@/components/builder/dialogs/DownloadDialog.vue';
import { exportFileName } from '@/builder/exporters.js';
import { useBuilderStore } from '@/builder/store.js';
import { afterPaint, busyTask } from '@/builder/busy.js';
import { builderSettings } from '@/builder/settings.js';
import { iconLibrary } from '@/builder/iconLibrary.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

// Promises a test has not settled. afterEach settles them, so that the
// shared download task of a failed test does not stay busy for the next.
const pending = [];

// A promise and the function that settles it.
function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });

  pending.push(resolve);

  return { promise, resolve };
}

function open(props = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = sampleDocument().doc;

  const wrapper = mount(DownloadDialog, {
    attachTo: document.body,
    props: { viewportElement: () => document.createElement('div'), ...props },
    global: { plugins: [pinia] },
  });

  return { wrapper, store };
}

const button = (wrapper, format) =>
  wrapper.get(`[data-testid="download-${format}"]`);
const statusOf = (wrapper) => wrapper.get('[role="status"]').text();

// The browser paints, then the work starts (see afterPaint in busy.js).
// This paint comes after the one that the work waits for.
async function paint() {
  await afterPaint();
  await flushPromises();
}

beforeEach(() => {
  exporters.exportImage.mockReset();
  exporters.saveText.mockReset();
});

afterEach(async () => {
  for (const resolve of pending.splice(0)) {
    resolve('data:,');
  }
  await vi.waitFor(() => expect(busyTask('download')).toBe(''));
});

describe('the Download dialog while a format is made', () => {
  test('PNG shows a ring in place of its icon and keeps its name, and the others wait', async () => {
    const image = deferred();

    exporters.exportImage.mockReturnValue(image.promise);

    const { wrapper, store } = open();
    const png = button(wrapper, 'png');

    png.element.focus();
    await png.trigger('click');

    // At once, before the image holds the page.
    expect(statusOf(wrapper)).toBe('Preparing PNG…');
    expect(png.attributes('aria-busy')).toBe('true');
    expect(png.find('[data-testid="builder-spinner"]').exists()).toBe(true);
    expect(png.find('.builder-icon').exists()).toBe(false);
    expect(png.text()).toBe('PNG');
    expect(png.attributes('aria-label')).toBeUndefined();
    expect(document.activeElement).toBe(png.element);
    for (const format of [
      'json',
      'yaml',
      'topology-yaml',
      'svg',
      'drawio',
      'gexf',
    ]) {
      expect(button(wrapper, format).attributes('aria-disabled'), format).toBe(
        'true',
      );
      expect(button(wrapper, format).attributes('aria-busy'), format).toBe(
        undefined,
      );
    }

    await vi.waitFor(() =>
      expect(exporters.exportImage).toHaveBeenCalledOnce(),
    );

    // A second press, and a press on another format, start nothing.
    await png.trigger('click');
    await button(wrapper, 'svg').trigger('click');
    await paint();
    expect(exporters.exportImage).toHaveBeenCalledOnce();
    expect(statusOf(wrapper)).toBe('Preparing PNG…');

    image.resolve('data:image/png;base64,');
    await flushPromises();

    expect(statusOf(wrapper)).toBe(
      `Saved ${exportFileName(store.doc, 'png')}.`,
    );
    expect(png.attributes('aria-busy')).toBeUndefined();
    expect(png.find('.builder-icon').exists()).toBe(true);
    expect(button(wrapper, 'svg').attributes('aria-disabled')).toBeUndefined();
    expect(document.activeElement).toBe(png.element);
  });

  test('a format started by a palette command shows its ring on its button', async () => {
    const image = deferred();

    exporters.exportImage.mockReturnValue(image.promise);

    const { wrapper } = open({ start: 'svg' });

    await flushPromises();

    const svg = button(wrapper, 'svg');

    expect(document.activeElement).toBe(svg.element);
    expect(svg.attributes('aria-busy')).toBe('true');
    expect(statusOf(wrapper)).toBe('Preparing SVG…');

    await vi.waitFor(() =>
      expect(exporters.exportImage).toHaveBeenCalledWith(
        expect.objectContaining({ format: 'svg' }),
      ),
    );
    image.resolve('data:image/svg+xml,');
    await flushPromises();
    expect(svg.attributes('aria-busy')).toBeUndefined();
  });

  test('Builder JSON shows its ring while the icon library is read', async () => {
    const { wrapper } = open();
    const json = button(wrapper, 'json');

    await json.trigger('click');
    expect(json.attributes('aria-busy')).toBe('true');
    expect(statusOf(wrapper)).toBe('Preparing Builder JSON…');

    await vi.waitFor(() => expect(exporters.saveText).toHaveBeenCalledOnce());
    await vi.waitFor(() =>
      expect(json.attributes('aria-busy')).toBeUndefined(),
    );
    expect(statusOf(wrapper)).toMatch(/^Saved .*\.json\./);
  });

  test('draw.io saves the diagram as an mxfile in the light colors, with the node icon setting', async () => {
    const { wrapper, store } = open();
    const drawio = button(wrapper, 'drawio');

    store.resolvedTheme = 'dark';
    builderSettings.showNodeIcons = false;

    try {
      await drawio.trigger('click');
      expect(drawio.attributes('aria-busy')).toBe('true');
      expect(statusOf(wrapper)).toBe('Preparing draw.io…');

      await vi.waitFor(() => expect(exporters.saveText).toHaveBeenCalledOnce());

      const [{ text, mime, fileName }] = exporters.saveText.mock.calls[0];

      expect(fileName).toBe(exportFileName(store.doc, 'drawio'));
      expect(mime).toBe('application/vnd.jgraph.mxfile');
      expect(text).toMatch(/^<mxfile host="phenix" type="device">/);
      expect(text).toContain('background="#ffffff"');
      // Devices and switches draw no icon while the setting is off.
      expect(text).not.toMatch(/style="[^"]*image=/);
      await vi.waitFor(() =>
        expect(drawio.attributes('aria-busy')).toBeUndefined(),
      );
      expect(statusOf(wrapper)).toBe(`Saved ${fileName}.`);
    } finally {
      builderSettings.showNodeIcons = true;
    }
  });

  test('draw.io says which custom icons it saved without their images', async () => {
    const { wrapper, store } = open();
    const [device] = store.doc.nodes.filter((node) => node.kind === 'device');

    store.doc = {
      ...store.doc,
      nodes: store.doc.nodes.map((node) =>
        node === device
          ? { ...node, device: { ...node.device, icon: 'plc' } }
          : node,
      ),
    };
    // The library lists plc, and its image could not be read.
    vi.spyOn(iconLibrary, 'ensureImages').mockResolvedValue();
    vi.spyOn(iconLibrary, 'lookup').mockImplementation((name) =>
      name === 'plc' ? { name: 'plc', id: 'p1' } : null,
    );

    try {
      await button(wrapper, 'drawio').trigger('click');
      await vi.waitFor(() => expect(exporters.saveText).toHaveBeenCalledOnce());
      await vi.waitFor(() =>
        expect(statusOf(wrapper)).toBe(
          `Saved ${exportFileName(store.doc, 'drawio')}. The Builder could not read the image of the custom icon plc. The file does not show it. Try again later.`,
        ),
      );
    } finally {
      vi.restoreAllMocks();
    }
  });

  test('edits the Inspector cannot apply disable every format', async () => {
    const { wrapper } = open({
      unapplied: { title: 'Device alpha', fields: ['Memory'] },
    });

    for (const format of ['json', 'png', 'drawio', 'gexf', 'package']) {
      const target = button(wrapper, format);

      expect(target.attributes('aria-disabled'), format).toBe('true');
      expect(target.attributes('disabled'), format).toBe('');
      await target.trigger('click');
    }
    await paint();

    expect(exporters.exportImage).not.toHaveBeenCalled();
    expect(exporters.saveText).not.toHaveBeenCalled();
    expect(statusOf(wrapper)).toBe('');
  });
});
