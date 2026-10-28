// @vitest-environment happy-dom
// Download embeds the custom icons after reading the library, and Upload
// holds on icon warnings. Each dialog is mounted and driven as a user would.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import YAML from 'js-yaml';

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
import { PACKAGE_SCHEMA_URI } from '@/builder/package.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';
import { base64Of, ICON_DATA, png } from './png.js';

// Two icons of their own bytes: the server's hmi, and the diagram's.
const THEIRS = base64Of(png(2, 2, [200, 40, 80, 255]));
const OURS = base64Of(png(2, 2, [10, 120, 40, 255]));

const CLASH =
  "The server already has an icon named hmi that differs from this diagram's. The diagram keeps its own copy, which it shows in place of the server's.";

enableAutoUnmount(afterEach);

// Opens a dialog in the page, as Builder does, after `prepare` sets up the
// Builder store.
function open(component, prepare = () => {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  prepare(store);

  const wrapper = mount(component, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store };
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

// The text of a dialog's role=status region.
const statusOf = (wrapper) => wrapper.get('[role="status"]').text();

beforeEach(() => {
  vi.restoreAllMocks();
  saved.files = [];
  libraryHolds([]);
});

describe('the Download dialog', () => {
  test('reads the icon library before a Builder JSON file embeds the icons the diagram uses', async () => {
    // The library has the icons, and their images, only once they are read.
    const ensure = vi
      .spyOn(iconLibrary, 'ensureImages')
      .mockImplementation(async () => {
        libraryHolds([
          { name: 'plc', aliases: [], data: ICON_DATA },
          { name: 'HMI', aliases: [], data: THEIRS },
        ]);
      });
    const { wrapper } = open(DownloadDialog, (store) => {
      store.doc = namingIcons();
    });

    await wrapper.get('[data-testid="download-json"]').trigger('click');
    // The file is made once the button's ring is painted (see busy.js).
    await vi.waitFor(() => expect(saved.files).toHaveLength(1));

    expect(ensure).toHaveBeenCalledExactlyOnceWith(new Set(['plc', 'hmi']));
    expect(saved.files.map((file) => file.fileName)).toEqual(['sample.json']);

    const file = JSON.parse(await saved.files[0].blob.text());

    expect(file.icons).toEqual({
      plc: { data: ICON_DATA },
      hmi: { data: THEIRS },
    });
    expect(statusOf(wrapper)).toBe('Saved sample.json.');
  });

  test('a library that cannot be read leaves out the icons the diagram has no copy of, and says so', async () => {
    // The library cannot be read: it holds no icon.
    vi.spyOn(iconLibrary, 'ensureImages').mockResolvedValue();

    const { wrapper } = open(DownloadDialog, (store) => {
      store.doc = { ...namingIcons(), icons: { hmi: { data: OURS } } };
    });

    await wrapper.get('[data-testid="download-yaml"]').trigger('click');
    await vi.waitFor(() => expect(saved.files).toHaveLength(1));

    const file = YAML.load(await saved.files[0].blob.text());

    expect(saved.files[0].fileName).toBe('sample.yaml');
    expect(file.icons).toEqual({ hmi: { data: OURS } });
    expect(statusOf(wrapper)).toBe(
      "Saved sample.yaml. It does not carry the custom icon plc. The server's icon library does not have it. The nodes that name it show their built-in icon.",
    );
    expect(wrapper.find('[data-testid="download-error"]').exists()).toBe(false);
  });
});

describe('the Upload dialog', () => {
  // Opens the dialog and uploads `doc` as pasted text, as upload-dialog.test.js
  // does. Resolves to the dialog, the store, and its draft before the upload.
  async function upload(doc) {
    const { wrapper, store } = open(UploadDialog);
    const before = store.doc;

    await wrapper.get('input[value="text"]').setValue();
    await wrapper
      .get('[data-testid="upload-text"]')
      .setValue(JSON.stringify(doc));
    await wrapper.get('[data-testid="upload-submit"]').trigger('click');
    await flushPromises();

    return { wrapper, store, before };
  }

  // The texts of the listed warnings, without their "Warning:" label.
  const warningsOf = (wrapper) =>
    wrapper
      .findAll('[data-testid="upload-warnings"] li')
      .map((item) => item.text().replace(/^Warning:\s*/, ''));

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
    const { wrapper, store, before } = await upload(doc);

    // plc, which the library lacks, is added under its name. hmi, which it
    // has with other bytes, gives a warning.
    expect(sent).toHaveBeenCalledTimes(1);
    expect(sent).toHaveBeenCalledWith({ name: 'plc', data: ICON_DATA });
    expect(warningsOf(wrapper)).toEqual([CLASH]);
    expect(wrapper.get('[data-testid="upload-result"] > p').text()).toBe(
      'This upload has 1 warning.',
    );
    expect(statusOf(wrapper)).toBe('This upload has 1 warning.');
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="upload-continue"]').element,
    );

    // Nothing is made until the user goes on.
    expect(wrapper.emitted('uploaded')).toBeUndefined();
    expect(store.doc).toBe(before);

    await wrapper.get('[data-testid="upload-continue"]').trigger('click');

    expect(store.doc.icons).toEqual({ hmi: { data: OURS } });
    expect(
      store.doc.nodes.map((node) => node.device?.icon).filter(Boolean),
    ).toEqual(['plc', 'hmi']);
    expect(wrapper.emitted('uploaded')).toEqual([[{}]]);
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('a package whose configs were created before an icon warning names them with it, and again on Cancel', async () => {
    vi.spyOn(iconLibrary, 'load').mockImplementation(async () =>
      libraryHolds([{ name: 'hmi', aliases: [], data: THEIRS }]),
    );
    vi.spyOn(iconLibrary, 'upload').mockImplementation(async (icon) => ({
      icon: { ...icon },
      created: true,
    }));

    const scenario = {
      apiVersion: 'phenix.sandia.gov/v2',
      kind: 'Scenario',
      metadata: { name: 'plant-ntp' },
      spec: { apps: [{ name: 'ntp' }] },
    };
    const pkg = {
      $schema: PACKAGE_SCHEMA_URI,
      document: { ...namingIcons(), icons: { hmi: { data: OURS } } },
      scenarios: { 'plant-ntp': scenario },
      requirements: {
        scenarios: ['plant-ntp'],
        topologies: [],
        templates: [],
        icons: ['hmi', 'plc'],
        images: [],
        apps: ['ntp'],
        files: [],
      },
    };
    const { wrapper, store } = open(UploadDialog, (builder) => {
      // The list of what the package needs, as the server answers it.
      vi.spyOn(builder, 'resolvePackage').mockResolvedValue({
        dependencies: [
          {
            kind: 'scenario',
            name: 'plant-ntp',
            status: 'missing',
            packaged: true,
          },
        ],
      });
    });
    const create = vi
      .spyOn(store, 'createPackagedConfig')
      .mockResolvedValue(undefined);

    const field = wrapper.get('[data-testid="upload-file"]');

    Object.defineProperty(field.element, 'files', {
      configurable: true,
      value: [
        new File([JSON.stringify(pkg)], 'plant.json', {
          type: 'application/json',
        }),
      ],
    });
    await field.trigger('change');
    await flushPromises();
    await wrapper.get('[data-testid="upload-submit"]').trigger('click');
    await flushPromises();

    await wrapper
      .get('[data-testid="upload-package-create-scenario-plant-ntp"]')
      .setValue(true);
    await wrapper
      .get('[data-testid="upload-package-continue"]')
      .trigger('click');
    await flushPromises();

    // The config is created, and hmi, which the server has with other
    // bytes, gives a warning: the warnings name what the server now has.
    const summary =
      'Created Scenario config plant-ntp on this server. This upload has 1 warning.';

    expect(create).toHaveBeenCalledTimes(1);
    expect(create).toHaveBeenCalledWith(scenario);
    expect(warningsOf(wrapper)).toEqual([CLASH]);
    expect(wrapper.get('[data-testid="upload-result"] > p').text()).toBe(
      summary,
    );
    expect(statusOf(wrapper)).toBe(summary);
    expect(wrapper.emitted('uploaded')).toBeUndefined();

    // Cancel opens nothing, and says what stays on the server.
    await wrapper.get('[data-testid="upload-cancel"]').trigger('click');

    expect(store.announcement).toBe(
      'Created Scenario config plant-ntp on this server.',
    );
    expect(wrapper.emitted('close')).toHaveLength(1);
    expect(wrapper.emitted('uploaded')).toBeUndefined();
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
    const { wrapper, store } = await upload({
      ...namingIcons(),
      icons: { plc: { data: ICON_DATA }, hmi: { data: OURS } },
    });

    expect(sent).toHaveBeenCalledTimes(1);
    expect(wrapper.find('[data-testid="upload-warnings"]').exists()).toBe(
      false,
    );
    expect(wrapper.emitted('uploaded')).toEqual([[{}]]);
    expect(store.doc).not.toHaveProperty('icons');
  });
});
