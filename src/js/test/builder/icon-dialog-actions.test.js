// @vitest-environment happy-dom
// The Custom icons dialog and its Upload icon…, mounted and used: Use,
// Rename, Add to server, the rows' keys and selection, the batches, and an
// upload.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';

import IconUpload from '@/components/builder/IconUpload.vue';
import IconDialog from '@/components/builder/dialogs/IconDialog.vue';
import { useMessage } from '@/components/builder/dialogs/message.js';
import {
  INSPECTOR_ICON_LIBRARY,
  INSPECTOR_ICONS,
} from '@/components/builder/inspector/control.js';
import { ICON_NAME_HINT, IconFileError } from '@/builder/icons.js';

import { base64Of, ICON_DATA, png } from './png.js';

const OTHER = base64Of(png(3, 2, [9, 9, 9, 255]));
const BAD_NAME = `Enter a name for the icon. ${ICON_NAME_HINT}`;

// hmi and pump the user may change. Valve is the server's own.
const ICONS = [
  ...['hmi', 'pump'].map((name) => ({
    name,
    owner: 'alice',
    width: 1,
    height: 1,
    bytes: 70,
    aliases: [],
    data: ICON_DATA,
    canRename: true,
    canDelete: true,
  })),
  {
    name: 'valve',
    owner: '',
    width: 3,
    height: 2,
    bytes: 80,
    aliases: [],
    data: OTHER,
    canRename: false,
    canDelete: false,
  },
];

enableAutoUnmount(afterEach);

// The server's library, whose state the calls change as the server would.
// `fails` names the icons whose change the server refuses.
function libraryWith({ icons = ICONS, fails = [] } = {}) {
  const refuse = (name) => {
    if (fails.includes(name)) {
      throw new Error(`The server refused ${name}.`);
    }
  };
  const library = {
    state: reactive({
      status: 'ready',
      error: '',
      icons: [...icons],
      maxIcons: 2000,
    }),
    load: vi.fn(async () => {}),
    ensure: vi.fn(async () => {}),
    lookup: (name) =>
      library.state.icons.find(
        (icon) => icon.name.toLowerCase() === name.toLowerCase(),
      ) || null,
    upload: vi.fn(async ({ name, data }) => {
      refuse(name);

      const icon = { ...ICONS[0], name, data };

      library.state.icons = [...library.state.icons, icon];

      return { icon, created: true };
    }),
    rename: vi.fn(async (name, to) => {
      refuse(name);
      library.state.icons = library.state.icons.map((icon) =>
        icon.name === name ? { ...icon, name: to, aliases: [name] } : icon,
      );

      return { name: to };
    }),
    failure: (caught) => caught.message,
  };

  return library;
}

function openDialog({ library = libraryWith(), diagram = [], rasterize } = {}) {
  const wrapper = mount(IconDialog, {
    attachTo: document.body,
    props: rasterize ? { rasterize } : {},
    global: {
      provide: {
        [INSPECTOR_ICONS]: { entry: () => undefined, diagram: () => diagram },
        [INSPECTOR_ICON_LIBRARY]: library,
      },
    },
  });

  return { wrapper, library };
}

const button = (wrapper, name) => wrapper.get(`button[aria-label="${name}"]`);
const rowOf = (wrapper, list, name) =>
  wrapper.get(`[data-testid="${list}"] > li[data-icon="${name}"]`);
const boxOf = (wrapper, list, name) =>
  rowOf(wrapper, list, name).get('[data-testid="icon-select"]');
const statusOf = (wrapper) =>
  wrapper.find('[data-testid="icon-status"]').text();
const errorOf = (wrapper) => wrapper.find('[data-testid="icon-error"]').text();
const confirmOf = (wrapper) => wrapper.find('[data-testid="builder-confirm"]');

// Chooses `file` in the hidden file field, as the file picker does.
async function choose(wrapper, file, prefix = 'icon') {
  const field = wrapper.get(`[data-testid="${prefix}-file"]`);

  Object.defineProperty(field.element, 'files', {
    configurable: true,
    value: [file],
  });
  await field.trigger('change');
  await flushPromises();
}

const svgFile = (name) =>
  new File(['<svg xmlns="http://www.w3.org/2000/svg"/>'], name, {
    type: 'image/svg+xml',
  });

describe('the Custom icons dialog', () => {
  test('reads the library when it opens, and Use gives the field the chosen icon', async () => {
    const { wrapper, library } = openDialog();

    expect(library.load).toHaveBeenCalledOnce();
    // Use has an icon before its label, as Rename beside it has.
    expect(
      button(wrapper, 'Use pump').find('.builder-icon--check').exists(),
    ).toBe(true);
    await button(wrapper, 'Use pump').trigger('click');
    expect(wrapper.emitted('use')).toEqual([[{ name: 'pump' }]]);
  });

  test('Rename opens a form on the icon’s name, refuses a bad name with focus on the field, and Cancel gives focus back', async () => {
    const { wrapper, library } = openDialog();
    const rename = button(wrapper, 'Rename hmi');

    rename.element.focus();
    await rename.trigger('click');
    await flushPromises();

    const field = wrapper.get('[data-testid="icon-rename-name"]');

    expect(wrapper.get('#icon-rename-title').text()).toBe('Rename hmi');
    expect(field.element.value).toBe('hmi');
    expect(document.activeElement).toBe(field.element);

    await field.setValue('not a name!');
    rename.element.focus();
    await wrapper.get('[data-testid="icon-rename-form"]').trigger('submit');
    expect(errorOf(wrapper)).toBe(BAD_NAME);
    expect(document.activeElement).toBe(field.element);
    expect(library.rename).not.toHaveBeenCalled();

    await wrapper.get('[data-testid="icon-rename-cancel"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="icon-rename-form"]').exists()).toBe(
      false,
    );
    expect(document.activeElement).toBe(rename.element);

    // The same name changes nothing, and closes the form.
    await rename.trigger('click');
    await wrapper.get('[data-testid="icon-rename-form"]').trigger('submit');
    await flushPromises();
    expect(wrapper.find('[data-testid="icon-rename-form"]').exists()).toBe(
      false,
    );
    expect(library.rename).not.toHaveBeenCalled();
  });

  test('Rename renames the icon and focuses its Rename, and a refused name keeps the form', async () => {
    const { wrapper, library } = openDialog({
      library: libraryWith({ fails: ['pump'] }),
    });

    await button(wrapper, 'Rename hmi').trigger('click');
    await wrapper.get('[data-testid="icon-rename-name"]').setValue('panel');
    await wrapper.get('[data-testid="icon-rename-form"]').trigger('submit');
    await flushPromises();

    expect(library.rename).toHaveBeenCalledWith('hmi', 'panel');
    expect(statusOf(wrapper)).toBe(
      'Renamed hmi to panel. hmi keeps working as another name of it.',
    );
    expect(document.activeElement).toBe(
      button(wrapper, 'Rename panel').element,
    );

    await button(wrapper, 'Rename pump').trigger('click');
    await wrapper.get('[data-testid="icon-rename-name"]').setValue('motor');
    wrapper.get('[data-testid="icon-rename-submit"]').element.focus();
    await wrapper.get('[data-testid="icon-rename-form"]').trigger('submit');
    await flushPromises();

    expect(errorOf(wrapper)).toBe('The server refused pump.');
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="icon-rename-name"]').element,
    );
  });

  test('Add to server shows a ring on its own button while the server adds the icon, and ignores a second press', async () => {
    const library = libraryWith();
    let answer;

    library.upload.mockImplementationOnce(
      ({ name, data }) =>
        new Promise((resolve) => {
          answer = () =>
            resolve({ icon: { ...ICONS[0], name, data }, created: true });
        }),
    );

    const { wrapper } = openDialog({
      library,
      diagram: [
        { name: 'plc', data: OTHER },
        { name: 'rtu', data: OTHER },
      ],
    });
    const plc = button(wrapper, 'Add plc to the server');

    await plc.trigger('click');
    await plc.trigger('click');

    expect(library.upload).toHaveBeenCalledTimes(1);
    expect(plc.attributes('aria-busy')).toBe('true');
    expect(plc.find('[data-testid="builder-spinner"]').exists()).toBe(true);
    expect(plc.text()).toBe('Add to server');

    const rtu = button(wrapper, 'Add rtu to the server');

    expect(rtu.attributes('aria-busy')).toBeUndefined();
    expect(rtu.attributes('aria-disabled')).toBe('true');

    answer();
    await flushPromises();
    expect(
      button(wrapper, 'Add rtu to the server').attributes('aria-disabled'),
    ).toBeUndefined();
  });
});

describe('the Custom icons dialog’s lists', () => {
  // Three copies that the server lacks, so each can be selected.
  const COPIES = ['plc', 'rtu', 'tank'].map((name) => ({ name, data: OTHER }));

  test('the server icons are one Tab stop that Home and End move, and nothing in them is selected or deleted', async () => {
    const { wrapper } = openDialog();
    const rows = () =>
      wrapper.findAll('[data-testid="icon-library-list"] > li');
    const stops = () =>
      rows()
        .filter((row) => row.attributes('tabindex') === '0')
        .map((row) => row.element);

    expect(stops()).toEqual([rows()[0].element]);

    rows()[0].element.focus();
    await rows()[0].trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(rows()[2].element);
    expect(stops()).toEqual([rows()[2].element]);

    await rows()[2].trigger('keydown', { key: 'Home' });
    expect(document.activeElement).toBe(rows()[0].element);

    // The Icons tab deletes icons. Here a row has Use and Rename only.
    expect(
      wrapper
        .find('[data-testid="icon-library-list"] [data-testid="icon-select"]')
        .exists(),
    ).toBe(false);
    expect(wrapper.find('[data-testid="icon-delete"]').exists()).toBe(false);
    expect(
      wrapper.find('[data-testid="bulk-bar-icons-library"]').exists(),
    ).toBe(false);
    expect(rows()[0].attributes('aria-labelledby')).toBe('icon-name-library-0');

    await rows()[0].trigger('keydown', { key: ' ' });
    await rows()[0].trigger('keydown', { key: 'Delete' });
    expect(wrapper.find('[data-testid="icon-status"]').exists()).toBe(false);
    expect(confirmOf(wrapper).exists()).toBe(false);

    // With nothing selected, Escape is the dialog's.
    await rows()[0].trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('in the copies, Space selects, and Escape clears and leaves the dialog open', async () => {
    const { wrapper } = openDialog({ diagram: COPIES });
    const plc = rowOf(wrapper, 'icon-diagram-list', 'plc');

    plc.element.focus();
    await plc.trigger('keydown', { key: ' ' });
    expect(boxOf(wrapper, 'icon-diagram-list', 'plc').element.checked).toBe(
      true,
    );
    expect(statusOf(wrapper)).toBe('1 of 3 selected.');

    await plc.trigger('keydown', { key: 'Escape' });
    expect(statusOf(wrapper)).toBe('0 of 3 selected.');
    expect(wrapper.emitted('close')).toBeUndefined();

    await plc.trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('close')).toHaveLength(1);
  });

  test('a checkbox selects its copy, Shift a range, and Select all every copy, then none', async () => {
    const { wrapper } = openDialog({ diagram: COPIES });
    const countOf = () =>
      wrapper.get('[data-testid="bulk-count-icons-diagram"]').text();

    await boxOf(wrapper, 'icon-diagram-list', 'plc').setValue(true);
    expect(countOf()).toBe('1 of 3 selected');
    await boxOf(wrapper, 'icon-diagram-list', 'tank').trigger('click', {
      shiftKey: true,
    });
    expect(countOf()).toBe('3 of 3 selected');
    expect(boxOf(wrapper, 'icon-diagram-list', 'rtu').element.checked).toBe(
      true,
    );

    const all = wrapper.get('[data-testid="bulk-all-icons-diagram"]');

    await all.setValue(false);
    expect(countOf()).toBe('0 of 3 selected');
    expect(statusOf(wrapper)).toBe('0 of 3 selected.');
    await all.setValue(true);
    expect(statusOf(wrapper)).toBe('3 of 3 selected.');
  });

  test('a failed Add selected to server is listed under its row, with focus', async () => {
    const { wrapper } = openDialog({
      library: libraryWith({ fails: ['rtu'] }),
      diagram: COPIES.slice(0, 2),
    });

    await wrapper.get('[data-testid="bulk-all-icons-diagram"]').setValue(true);
    await wrapper
      .get('[data-testid="bulk-add-icons-diagram"]')
      .trigger('click');
    await flushPromises();

    const summary = wrapper.get('[data-testid="bulk-summary"]');

    expect(summary.get('h3').text()).toBe(
      '1 of 2 icons could not be added to the server. The other 1 was added to the server.',
    );
    expect(summary.text()).toContain('rtu: The server refused rtu.');
    expect(document.activeElement).toBe(summary.element);
  });

  test('Add selected to server adds the diagram’s copies the server lacks', async () => {
    const diagram = [
      { name: 'hmi', data: ICON_DATA },
      { name: 'plc', data: OTHER },
    ];
    const { wrapper, library } = openDialog({ diagram });

    // The server has hmi as it is: only plc can be selected.
    expect(
      rowOf(wrapper, 'icon-diagram-list', 'hmi')
        .find('[data-testid="icon-select"]')
        .exists(),
    ).toBe(false);

    await boxOf(wrapper, 'icon-diagram-list', 'plc').setValue(true);
    await wrapper
      .get('[data-testid="bulk-add-icons-diagram"]')
      .trigger('click');
    await flushPromises();

    expect(library.upload).toHaveBeenCalledWith({ name: 'plc', data: OTHER });
    expect(statusOf(wrapper)).toBe(
      'Added 1 icon to the server. The diagram drops its copy with its next edit.',
    );
  });
});

describe('Upload icon…', () => {
  function mountUpload({ library = libraryWith(), rasterize } = {}) {
    const status = useMessage();
    const error = useMessage();
    const wrapper = mount(IconUpload, {
      attachTo: document.body,
      props: { library, status, error, rasterize, prefix: 'tab' },
    });

    return { wrapper, library, status, error };
  }

  test('the button opens the file chooser', async () => {
    const { wrapper } = mountUpload();
    const click = vi.spyOn(
      wrapper.get('[data-testid="tab-file"]').element,
      'click',
    );

    await wrapper.get('[data-testid="tab-upload"]').trigger('click');
    expect(click).toHaveBeenCalledOnce();
  });

  test('a chosen file is converted, named after the file, and Add icon sends it to the library', async () => {
    const rasterize = vi.fn(async () => ({ name: 'panel', data: ICON_DATA }));
    const { wrapper, library, status } = mountUpload({ rasterize });
    const file = svgFile('panel.svg');

    await choose(wrapper, file, 'tab');

    const name = wrapper.get('[data-testid="tab-upload-name"]');

    expect(rasterize).toHaveBeenCalledWith(file);
    expect(name.element.value).toBe('panel');
    expect(document.activeElement).toBe(name.element);
    expect(wrapper.emitted('update:busy')).toEqual([['convert'], ['']]);

    await wrapper.get('[data-testid="tab-upload-form"]').trigger('submit');
    await flushPromises();

    expect(library.upload).toHaveBeenCalledWith({
      name: 'panel',
      data: ICON_DATA,
    });
    expect(status.text).toBe('Added panel to the server.');
    expect(wrapper.emitted('uploaded')).toEqual([
      [{ icon: expect.objectContaining({ name: 'panel' }), created: true }],
    ]);
    expect(wrapper.find('[data-testid="tab-upload-form"]').exists()).toBe(
      false,
    );
  });

  test('a bad or refused name keeps the form with focus on the name, and Cancel goes back to the button', async () => {
    const rasterize = vi.fn(async () => ({ name: 'pump', data: ICON_DATA }));
    const { wrapper, library, error } = mountUpload({
      library: libraryWith({ fails: ['pump'] }),
      rasterize,
    });

    await choose(wrapper, svgFile('pump.svg'), 'tab');

    const name = wrapper.get('[data-testid="tab-upload-name"]');

    await name.setValue('..');
    wrapper.get('[data-testid="tab-upload-submit"]').element.focus();
    await wrapper.get('[data-testid="tab-upload-form"]').trigger('submit');
    expect(error.text).toBe(BAD_NAME);
    expect(name.attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(name.element);
    expect(library.upload).not.toHaveBeenCalled();

    await name.setValue('pump');
    wrapper.get('[data-testid="tab-upload-cancel"]').element.focus();
    await wrapper.get('[data-testid="tab-upload-form"]').trigger('submit');
    await flushPromises();
    expect(error.text).toBe('The server refused pump.');
    expect(document.activeElement).toBe(name.element);

    await wrapper.get('[data-testid="tab-upload-cancel"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="tab-upload-form"]').exists()).toBe(
      false,
    );
    expect(document.activeElement).toBe(
      wrapper.get('[data-testid="tab-upload"]').element,
    );
  });

  test('a file that is not an image says why', async () => {
    const rasterize = vi.fn(async () => {
      throw new IconFileError('This file is not an image.');
    });
    const library = { ...libraryWith(), failure: () => 'The server failed.' };
    const { wrapper, error } = mountUpload({ library, rasterize });

    await choose(wrapper, svgFile('notes.svg'), 'tab');
    expect(error.text).toBe('This file is not an image.');
    expect(wrapper.find('[data-testid="tab-upload-form"]').exists()).toBe(
      false,
    );
  });

  test('in the dialog, the new icon’s Use takes focus after the upload', async () => {
    const rasterize = vi.fn(async () => ({ name: 'panel', data: OTHER }));
    const { wrapper } = openDialog({ rasterize });

    await choose(wrapper, svgFile('panel.svg'));
    await wrapper.get('[data-testid="icon-upload-form"]').trigger('submit');
    await flushPromises();

    expect(document.activeElement).toBe(button(wrapper, 'Use panel').element);
  });
});
