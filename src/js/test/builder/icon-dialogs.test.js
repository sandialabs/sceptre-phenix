// How the Download and Upload dialogs handle custom icons: a Builder JSON or
// YAML download reads the server's icon library before it embeds the icons
// the diagram uses, and an upload whose icons give warnings holds the draft
// on them until the user goes on. Vitest runs without a DOM, so each dialog
// is rendered on the server, and the functions its buttons call are run
// from what its setup returned.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia } from 'pinia';
import YAML from 'js-yaml';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

// The files the dialogs save, in order.
const saved = vi.hoisted(() => ({ files: [] }));

vi.mock('file-saver', () => ({
  saveAs: (blob, fileName) => saved.files.push({ blob, fileName }),
}));

import DownloadDialog from '@/components/builder/dialogs/DownloadDialog.vue';
import UploadDialog from '@/components/builder/dialogs/UploadDialog.vue';
import { iconLibrary } from '@/builder/iconLibrary.js';
import { indexIcons } from '@/builder/icons.js';
import { updateNode } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';
import { base64Of, ICON_DATA, png } from './png.js';

// Two icons of their own bytes: the server's hmi, and the diagram's.
const THEIRS = base64Of(png(2, 2, [200, 40, 80, 255]));
const OURS = base64Of(png(2, 2, [10, 120, 40, 255]));

const CLASH =
  "The server already has an icon named hmi that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.";

// Renders a dialog, and returns the bindings its setup returned (the
// functions and state its template uses) and the Builder store.
async function opened(component, props = {}, prepare = () => {}) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });
  let bindings = null;

  app.use(pinia);
  app.mixin({
    created() {
      if (this.$.type === component) {
        bindings = this.$.setupState;
      }
    },
  });

  const store = useBuilderStore(pinia);

  prepare(store);
  await renderToString(app);

  return { bindings, store };
}

// Makes the icon library hold these icons, as a read of it would.
function libraryHolds(icons) {
  iconLibrary.state.index = indexIcons(icons);

  return iconLibrary.state;
}

// The sample diagram, its alpha device naming plc and its bravo device hmi.
function namingIcons() {
  const sample = sampleDocument();
  const doc = updateNode(sample.doc, sample.alpha.id, {
    device: { icon: 'plc' },
  });

  return updateNode(doc, sample.bravo.id, { device: { icon: 'hmi' } });
}

beforeEach(() => {
  vi.restoreAllMocks();
  saved.files = [];
  libraryHolds([]);
});

describe('the Download dialog', () => {
  test('reads the icon library before a Builder JSON file embeds the icons the diagram uses', async () => {
    // The library has the icons only once it is read.
    const ensure = vi
      .spyOn(iconLibrary, 'ensure')
      .mockImplementation(async () =>
        libraryHolds([
          { name: 'plc', aliases: [], data: ICON_DATA },
          { name: 'HMI', aliases: [], data: THEIRS },
        ]),
      );
    const { bindings } = await opened(DownloadDialog, {}, (store) => {
      store.doc = namingIcons();
    });

    await bindings.downloadText('json');

    expect(ensure).toHaveBeenCalledTimes(1);
    expect(saved.files.map((file) => file.fileName)).toEqual(['sample.json']);

    const file = JSON.parse(await saved.files[0].blob.text());

    expect(file.icons).toEqual({
      plc: { data: ICON_DATA },
      hmi: { data: THEIRS },
    });
    expect(bindings.status.text).toBe('Saved sample.json.');
  });

  test('a library that cannot be read leaves out the icons the diagram has no copy of, and says so', async () => {
    vi.spyOn(iconLibrary, 'ensure').mockRejectedValue(new Error('offline'));

    const { bindings } = await opened(DownloadDialog, {}, (store) => {
      store.doc = { ...namingIcons(), icons: { hmi: { data: OURS } } };
    });

    await bindings.downloadText('yaml');

    const file = YAML.load(await saved.files[0].blob.text());

    expect(saved.files[0].fileName).toBe('sample.yaml');
    expect(file.icons).toEqual({ hmi: { data: OURS } });
    expect(bindings.status.text).toBe(
      "Saved sample.yaml. It does not carry the custom icon plc, which the server's icon library does not have: the nodes that name it show their built-in icon.",
    );
  });
});

describe('the Upload dialog', () => {
  // Opens the dialog, uploads `doc` as pasted text, and returns the
  // dialog's bindings, the store, and the events the dialog emitted.
  async function upload(doc) {
    const emitted = { uploaded: vi.fn(), close: vi.fn() };
    const { bindings, store } = await opened(UploadDialog, {
      onUploaded: emitted.uploaded,
      onClose: emitted.close,
    });

    bindings.form.source = 'text';
    bindings.form.text = JSON.stringify(doc);

    const before = store.doc;

    await bindings.submit();

    return { bindings, store, emitted, before };
  }

  test('holds the draft on the warnings its icons give, and makes it from what the library left once the user goes on', async () => {
    vi.spyOn(iconLibrary, 'load').mockImplementation(async () =>
      libraryHolds([{ name: 'hmi', aliases: [], data: THEIRS }]),
    );

    const sent = vi
      .spyOn(iconLibrary, 'upload')
      .mockImplementation(async (icon) => ({
        icon: { ...icon },
        created: true,
      }));
    const doc = {
      ...namingIcons(),
      icons: { plc: { data: ICON_DATA }, hmi: { data: OURS } },
    };
    const { bindings, store, emitted, before } = await upload(doc);

    // plc, which the library lacks, is added under its name; hmi, which it
    // has with other bytes, gives a warning.
    expect(sent).toHaveBeenCalledTimes(1);
    expect(sent).toHaveBeenCalledWith(
      { name: 'plc', data: ICON_DATA },
      { refresh: false },
    );
    expect(bindings.warnings).toEqual([CLASH]);
    expect(bindings.pendingKind).toBe('upload');
    expect(bindings.warningSummary).toBe('This upload has 1 warning.');

    // Nothing is made until the user goes on.
    expect(emitted.uploaded).not.toHaveBeenCalled();
    expect(store.doc).toBe(before);

    bindings.proceed();

    expect(store.doc.icons).toEqual({ hmi: { data: OURS } });
    expect(
      store.doc.nodes.map((node) => node.device?.icon).filter(Boolean),
    ).toEqual(['plc', 'hmi']);
    expect(emitted.uploaded).toHaveBeenCalledWith({});
    expect(emitted.close).toHaveBeenCalledTimes(1);
  });

  test('a package whose configs were created before an icon warning names them with it, and again on Cancel', async () => {
    vi.spyOn(iconLibrary, 'load').mockImplementation(async () =>
      libraryHolds([{ name: 'hmi', aliases: [], data: THEIRS }]),
    );
    vi.spyOn(iconLibrary, 'upload').mockImplementation(async (icon) => ({
      icon: { ...icon },
      created: true,
    }));

    const emitted = { uploaded: vi.fn(), close: vi.fn() };
    const { bindings, store } = await opened(UploadDialog, {
      onUploaded: emitted.uploaded,
      onClose: emitted.close,
    });
    const create = vi
      .spyOn(store, 'createPackagedConfig')
      .mockResolvedValue(undefined);
    const ticked = {
      kind: 'scenario',
      name: 'plant-ntp',
      status: 'missing',
      packaged: true,
      detail: '',
    };

    // The list of what the package needs, as the server answered it.
    bindings.packageView = {
      pkg: {
        document: { ...namingIcons(), icons: { hmi: { data: OURS } } },
        scenarios: {
          'plant-ntp': {
            apiVersion: 'phenix.sandia.gov/v2',
            kind: 'Scenario',
            metadata: { name: 'plant-ntp' },
            spec: { apps: [{ name: 'ntp' }] },
          },
        },
        topologies: {},
      },
      dependencies: [ticked],
      sourceFile: '',
    };

    await bindings.continuePackage([ticked]);

    // The config is created, and hmi, which the server has with other
    // bytes, gives a warning: the warnings name what the server now has.
    expect(create).toHaveBeenCalledTimes(1);
    expect(bindings.warnings).toEqual([CLASH]);
    expect(bindings.warningSummary).toBe(
      'Created Scenario config plant-ntp on this server. This upload has 1 warning.',
    );
    expect(bindings.status.text).toBe(bindings.warningSummary);
    expect(emitted.uploaded).not.toHaveBeenCalled();

    // Cancel opens nothing, and says what stays on the server.
    bindings.requestClose();

    expect(store.announcement).toBe(
      'Created Scenario config plant-ntp on this server.',
    );
    expect(emitted.close).toHaveBeenCalledTimes(1);
    expect(emitted.uploaded).not.toHaveBeenCalled();
  });

  test('makes the draft at once when the library takes every icon', async () => {
    vi.spyOn(iconLibrary, 'load').mockImplementation(async () =>
      libraryHolds([{ name: 'hmi', aliases: [], data: OURS }]),
    );

    const sent = vi
      .spyOn(iconLibrary, 'upload')
      .mockImplementation(async (icon) => ({
        icon: { ...icon },
        created: true,
      }));
    const { bindings, store, emitted } = await upload({
      ...namingIcons(),
      icons: { plc: { data: ICON_DATA }, hmi: { data: OURS } },
    });

    expect(sent).toHaveBeenCalledTimes(1);
    expect(bindings.warnings).toEqual([]);
    expect(emitted.uploaded).toHaveBeenCalledWith({});
    expect(store.doc).not.toHaveProperty('icons');
  });
});
