// BuilderIcon, a custom icon on a canvas node, and the Custom icons dialog,
// given a library of the test's own. The dialog's requests are e2e only.

import { describe, expect, test } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import BuilderIcon from '@/components/builder/BuilderIcon.vue';
import IconDialog from '@/components/builder/dialogs/IconDialog.vue';
import {
  INSPECTOR_ICON_LIBRARY,
  INSPECTOR_ICONS,
} from '@/components/builder/inspector/control.js';
import GroupNode from '@/components/builder/nodes/GroupNode.vue';

import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { kindMeta, nodeIcon, PALETTE } from '@/builder/catalog.js';
import { ICON_ORDER } from '@/builder/iconLabels.js';
import { SERVER_ICON_OWNER, iconOwnerText } from '@/builder/icons.js';
import { addNode, updateNode } from '@/builder/model.js';
import { keepUnchanged } from '@/builder/stable.js';

import { sampleDocument, tags, textOf } from './fixtures.js';
import { base64Of, ICON_DATA, ICON_KEY, png } from './png.js';

const SRC = `data:image/png;base64,${ICON_DATA}`;
const PLC = { data: ICON_DATA };

// The icon library as the nodes see it (see createIconLibrary).
function libraryOf(icons) {
  return {
    lookup: (name) =>
      icons.find(
        (icon) =>
          icon.name.toLowerCase() === String(name).toLowerCase() ||
          (icon.aliases || []).some(
            (alias) => alias.toLowerCase() === String(name).toLowerCase(),
          ),
      ) || null,
  };
}

async function render(component, props = {}, provides = []) {
  const app = createSSRApp({ render: () => h(component, props) });

  for (const [key, value] of provides) {
    app.provide(key, value);
  }

  return renderToString(app);
}

// The sample document with a custom icon on its alpha device and on a
// group, which carries a copy of it.
function iconDocument() {
  const sample = sampleDocument();
  const group = addNode(sample.doc, {
    kind: 'group',
    title: 'Zone',
    icon: 'plc',
  });

  return {
    ...sample,
    group: group.node,
    doc: {
      ...updateNode(group.doc, sample.alpha.id, { device: { icon: 'plc' } }),
      icons: { plc: PLC },
    },
  };
}

describe('the custom icon a node names', () => {
  test('is a device’s or a group’s, and only an icon name', () => {
    expect(nodeIcon({ kind: 'device', device: { icon: 'plc' } })).toBe('plc');
    expect(nodeIcon({ kind: 'group', group: { icon: 'PLC-2' } })).toBe('PLC-2');

    for (const node of [
      undefined,
      { kind: 'device', device: {} },
      { kind: 'device', device: { icon: '' } },
      { kind: 'device', device: { icon: ICON_KEY } },
      { kind: 'device', device: { icon: 'https://example.com/a.png' } },
      { kind: 'device', device: { icon: 'data:image/svg+xml;base64,AAAA' } },
      { kind: 'device', device: { icon: 7 } },
      { kind: 'group', group: { iconKey: 'firewall' } },
      // A switch and a note have none.
      { kind: 'switch', switch: { icon: 'plc' } },
      { kind: 'note', note: { icon: 'plc' } },
    ]) {
      expect(nodeIcon(node), JSON.stringify(node)).toBe('');
    }
  });

  test('a flow node has the address it is drawn from, or none', () => {
    const { doc, alpha, bravo, sw, group } = iconDocument();
    const data = (document, id, options) =>
      toFlowNodes(document, options).find((node) => node.id === id).data;

    expect(data(doc, alpha.id)).toMatchObject({
      iconKey: 'linux',
      iconSrc: SRC,
    });
    expect(data(doc, group.id).iconSrc).toBe(SRC);
    expect(data(doc, bravo.id).iconSrc).toBe('');
    expect(data(doc, sw.id).iconSrc).toBe('');

    // A document that carries no copy draws the icon library's icon of
    // that name, by its name or an alias...
    const bare = { ...doc };
    const other = base64Of(png(2, 2, [1, 2, 3, 255]));

    delete bare.icons;
    expect(
      data(bare, alpha.id, {
        library: libraryOf([{ name: 'PLC-2', aliases: ['plc'], data: other }]),
      }).iconSrc,
    ).toBe(`data:image/png;base64,${other}`);

    // ...and with neither, the built-in one: nothing is fetched for it.
    expect(data(bare, alpha.id, { library: libraryOf([]) }).iconSrc).toBe('');
    expect(data(bare, alpha.id, { library: null }).iconKey).toBe('linux');
  });

  // Vue Flow is handed only the nodes an edit changed.
  test('an edit elsewhere leaves a node with an icon the object it was', () => {
    const { doc, alpha, bravo } = iconDocument();
    const before = toFlowNodes(doc);
    const moved = updateNode(doc, bravo.id, { position: { x: 16, y: 320 } });
    const after = keepUnchanged(toFlowNodes(moved), before);
    const at = (list, id) => list.find((node) => node.id === id);

    expect(at(after, alpha.id)).toBe(at(before, alpha.id));
    expect(at(after, bravo.id)).not.toBe(at(before, bravo.id));

    // Another icon on the device is a change of that node alone.
    const other = png(2, 2, [1, 2, 3, 255]);
    const swapped = {
      ...updateNode(doc, alpha.id, { device: { icon: 'other' } }),
      icons: { ...doc.icons, other: { data: base64Of(other) } },
    };
    const next = keepUnchanged(toFlowNodes(swapped), before);

    expect(at(next, alpha.id).data.iconSrc).toBe(
      `data:image/png;base64,${base64Of(other)}`,
    );
    expect(at(next, bravo.id)).toBe(at(before, bravo.id));
  });
});
describe('BuilderIcon', () => {
  // An icon's drawing, without its classes.
  const drawing = async (name) =>
    (await render(BuilderIcon, { name })).replace(/ class="[^"]*"/g, '');

  test('draws a custom icon as an image, at the size asked for', async () => {
    const html = await render(BuilderIcon, {
      name: 'server',
      src: SRC,
      size: 16,
    });

    expect(tags(html, 'img')).toHaveLength(1);
    expect(html).toContain(`src="${SRC}"`);
    expect(html).toContain('class="builder-icon builder-icon--custom"');
    expect(html).toContain('width="16"');
    expect(html).toContain('height="16"');
    // A square, whatever the picture's proportions.
    expect(html).toContain('style="width:16px;height:16px;"');
    expect(html).toContain('aria-hidden="true"');
    expect(html).toContain('draggable="false"');
    // No markup of the built-in set, and none of any other kind.
    expect(html).not.toContain('<svg');
    expect(html).not.toContain('<path');
  });

  test('a titled custom icon is named by its alternative text', async () => {
    const html = await render(BuilderIcon, {
      name: 'server',
      src: SRC,
      title: 'plc "one"',
    });

    expect(html).toContain('alt="plc &quot;one&quot;"');
    expect(html).not.toContain('aria-hidden');
    // The size it is given when none is asked for, as the built-in icons.
    expect(html).toContain('style="width:18px;height:18px;"');
  });

  // Only a PNG data URL in base64 is ever the address of an image: any
  // other value draws the built-in icon of the name.
  test.each([
    ['a script URL', 'javascript:alert(1)'],
    ['an SVG data URL', `data:image/svg+xml;base64,${ICON_DATA}`],
    ['an HTML data URL', `data:text/html;base64,${ICON_DATA}`],
    ['a data URL with a space in it', `data:image/png;base64,iVBO Rw0K`],
    ['a data URL that ends its attribute', `${SRC}" onerror="alert(1)`],
    ['a server address', '/api/v1/builder/icons/abc'],
    ['an outside address', 'https://example.com/icon.png'],
    ['the icon’s data alone', ICON_DATA],
    ['nothing', ''],
  ])('%s as its address draws the built-in icon', async (_, src) => {
    const html = await render(BuilderIcon, { name: 'router', src });

    expect(tags(html, 'img')).toEqual([]);
    expect(html).toContain('class="builder-icon builder-icon--router"');
    expect(html).toContain('<svg');
    expect(html).not.toContain('onerror');
    expect(html).not.toContain('alert(1)');
    expect(html).not.toContain('example.com');
  });

  test('without an address it is the built-in icon, named by its title', async () => {
    const html = await render(BuilderIcon, { name: 'check', title: 'Done' });

    expect(html).toContain('role="img"');
    expect(html).toContain('aria-label="Done"');
    expect(html).toMatch(/<title[^>]*>Done<\/title>/);
    expect(tags(html, 'img')).toEqual([]);
  });

  // An unknown name draws the server icon. Each built-in key has a drawing
  // of its own, and no two keys share one.
  test('each built-in icon key has a drawing of its own', async () => {
    const fallback = await drawing('no-such-icon');
    const drawings = await Promise.all(ICON_ORDER.map(drawing));

    expect(fallback).toBe(await drawing('server'));
    ICON_ORDER.forEach((key, index) => {
      if (key !== 'server') {
        expect(drawings[index], key).not.toBe(fallback);
      }
    });
    expect(new Set(drawings).size).toBe(ICON_ORDER.length);
  });

  // The icon of each kind of node and of each palette entry has a drawing:
  // a key with a typo draws the server icon.
  test('each kind and each palette entry has an icon of its own', async () => {
    const fallback = await drawing('no-such-icon');
    const kinds = ['switch', 'note', 'group', 'shape', 'icon', 'line'];
    const keys = [
      ...kinds.map((kind) => kindMeta(kind).iconKey),
      ...PALETTE.map((entry) => entry.iconKey),
    ].filter((key) => key !== 'server');

    for (const key of keys) {
      expect(await drawing(key), key).not.toBe(fallback);
    }
  });

  // Fills and strokes follow the text color (theme, forced colors). Only the
  // Black Hat names a color of its own.
  test('a filled icon fills with the color of the text', async () => {
    for (const name of ['phenix', 'hacker']) {
      expect(await render(BuilderIcon, { name }), name).toContain(
        'fill="currentColor"',
      );
    }
    for (const name of ICON_ORDER.filter((key) => key !== 'blackhat')) {
      const html = await render(BuilderIcon, { name });
      const paints = [...html.matchAll(/(?:fill|stroke)="([^"]*)"/g)];

      expect(
        paints.every(([, paint]) => ['none', 'currentColor'].includes(paint)),
        name,
      ).toBe(true);
    }
  });

  // Its ink group is near-black in both themes, outlined in the text color
  // so it shows on a dark background.
  test('the Black Hat is dark in both themes', async () => {
    const html = await render(BuilderIcon, { name: 'blackhat' });
    const paints = [...html.matchAll(/(?:fill|stroke)="([^"]*)"/g)].map(
      ([, paint]) => paint,
    );

    expect(paints.filter((paint) => paint.startsWith('#'))).toEqual([
      '#111111',
    ]);
    expect(
      paints.every((paint) =>
        ['none', 'currentColor', '#111111'].includes(paint),
      ),
    ).toBe(true);
    expect(html).toMatch(/<g class="builder-icon__ink" fill="#111111"/);

    // No paint of its own inside the ink group: the outline is the color
    // of the text.
    const ink = html.match(/<g class="builder-icon__ink"[^>]*>(.*?)<\/g>/)[1];

    expect(ink).not.toMatch(/(?:fill|stroke)="/);
  });
});

describe('a custom icon on the canvas', () => {
  test('a group draws it in place of the icon of its key', async () => {
    const { doc, group } = iconDocument();
    const flow = toFlowNodes(doc).find((node) => node.id === group.id);
    const html = await render(GroupNode, { id: group.id, data: flow.data });

    expect(tags(html, 'img')).toHaveLength(1);
    expect(html).toContain(`src="${SRC}"`);
    expect(html).toContain('width="16"');
    expect(html).not.toContain('builder-icon--container');

    // Without the icon, the group's own.
    const plain = toFlowNodes({ ...doc, icons: {} }).find(
      (node) => node.id === group.id,
    );
    const bare = await render(GroupNode, { id: group.id, data: plain.data });

    expect(tags(bare, 'img')).toEqual([]);
    expect(bare).toContain('builder-icon--container');
  });
});

describe('the Custom icons dialog', () => {
  const other = png(3, 2, [9, 9, 9, 255]);
  const diagram = [
    { name: 'plc', data: ICON_DATA },
    { name: 'aaa', data: base64Of(other) },
  ];
  const server = [
    {
      name: 'hmi',
      owner: 'alice',
      width: 1,
      height: 1,
      bytes: 70,
      aliases: ['old-hmi'],
      data: ICON_DATA,
      canRename: true,
      canDelete: true,
    },
    {
      name: 'valve',
      owner: '',
      width: 3,
      height: 2,
      bytes: 80,
      aliases: [],
      data: base64Of(other),
      canRename: false,
      canDelete: false,
    },
  ];

  // The library the dialog is given: its state as the server last listed
  // it, and calls that never answer.
  function libraryWith(state) {
    const pending = () => new Promise(() => {});

    return {
      state: {
        status: 'loading',
        error: '',
        icons: [],
        maxIcons: 2000,
        ...state,
      },
      load: pending,
      ensure: pending,
      lookup: (name) =>
        (state.icons || []).find(
          (icon) => icon.name.toLowerCase() === name.toLowerCase(),
        ) || null,
      upload: pending,
      rename: pending,
      failure: () => 'failed',
    };
  }

  const provides = ({ icons = {}, library = {} } = {}) => [
    [
      INSPECTOR_ICONS,
      { entry: () => undefined, diagram: () => diagram, ...icons },
    ],
    [INSPECTOR_ICON_LIBRARY, libraryWith(library)],
  ];

  test('says what an icon is, and has one button to upload a file', async () => {
    const html = await render(IconDialog, {}, provides());

    expect(html).toMatch(/<h2 id="icon-dialog-title"[^>]*>Custom icons<\/h2>/);
    expect(html).toContain('aria-labelledby="icon-dialog-title"');
    expect(textOf(html)).toContain(
      'An icon is a small image that every user of this server can use. A node draws it at its icon size: 16, 24 or 32 pixels. PNG, JPEG, GIF, WebP and SVG files are converted to a PNG of at most 96 by 96 pixels.',
    );

    const [upload] = tags(html, 'button').filter((tag) =>
      tag.includes('data-testid="icon-upload"'),
    );
    const [file] = tags(html, 'input');

    expect(upload).toContain('type="button"');
    // The file field is not a Tab stop, and not announced: the button is.
    expect(tags(html, 'input')).toHaveLength(1);
    expect(file).toContain('type="file"');
    expect(file).toContain(' hidden');
    expect(file).toContain('tabindex="-1"');
    expect(file).toContain('aria-hidden="true"');
    expect(file).toContain(
      'accept=".png,.jpg,.jpeg,.gif,.webp,.svg,image/png,image/jpeg,image/gif,image/webp,image/svg+xml"',
    );
    // Both regions are there, empty, before any message.
    expect(html).toMatch(
      /<p class="builder-dialog__message" role="status"[^>]*><!----><\/p>/,
    );
    expect(html).toMatch(
      /<p id="icon-dialog-error" class="builder-dialog__message builder-dialog__error" role="alert"[^>]*><!----><\/p>/,
    );
    expect(textOf(html)).toMatch(/Close$/);
  });

  test('lists the diagram’s copies by name, each with its size and Use', async () => {
    const html = await render(IconDialog, {}, provides());
    const list = html.slice(
      html.indexOf('data-testid="icon-diagram-list"'),
      html.indexOf('data-testid="icon-library-heading"'),
    );
    const rows = list
      .split('<li')
      .slice(1)
      .map((row) => row.slice(0, row.indexOf('</li>')));

    expect(textOf(html)).toContain('In this diagram (2)');
    expect(rows).toHaveLength(2);
    // By name.
    expect(textOf(`<li${rows[0]}`)).toBe('aaa 3 × 2 pixels, 0.1 KiB Use');
    expect(rows[0]).toContain('aria-label="Use aaa"');
    expect(textOf(`<li${rows[1]}`)).toBe('plc 1 × 1 pixels, 0.1 KiB Use');

    // Each thumbnail is an image of the icon's PNG, and decoration.
    const images = tags(list, 'img');

    expect(images).toHaveLength(2);
    expect(images[0]).toContain(
      `src="data:image/png;base64,${base64Of(other)}"`,
    );
    expect(images[1]).toContain(`src="${SRC}"`);
    for (const image of images) {
      expect(image).toContain('width="32"');
      expect(image).toContain('aria-hidden="true"');
    }

    // Whether the server has a copy is not known until it is read.
    expect(list).not.toContain('Add to server');
    expect(list).not.toContain('On the server');
    expect(textOf(html)).toContain('Server icons (0)');
    expect(textOf(html)).toContain('Loading the server');
  });

  test('lists the server’s icons, with Rename for uploaded ones only and no Delete', async () => {
    const html = await render(
      IconDialog,
      {},
      provides({
        library: { status: 'ready', icons: server },
      }),
    );
    const list = html.slice(html.indexOf('data-testid="icon-library-list"'));
    const rows = list
      .split('<li')
      .slice(1)
      .map((row) => row.slice(0, row.indexOf('</li>')));

    const shown = (row) => textOf(`<li${row}`);

    expect(textOf(html)).toContain('Server icons (2)');
    // The count is of the server icons only, as the list is.
    expect(textOf(html)).toContain('The library holds 2 of 2000 icons.');
    expect(shown(rows[0])).toBe(
      'hmi Uploaded by alice · 1 × 1 pixels, 0.1 KiB · also named old-hmi Use Rename',
    );
    expect(rows[0]).toContain('aria-label="Rename hmi"');
    // Rename is the icon's Edit: blue, with the pencil, and keeps its label.
    const [rename] = tags(rows[0], 'button').filter((tag) =>
      tag.includes('data-testid="icon-rename"'),
    );

    expect(rename).toContain('builder-action-button--edit');
    expect(rename).toContain('builder-button--primary');
    expect(rows[0]).toContain('builder-icon--pencil');
    // The Icons tab deletes icons, not this dialog.
    expect(rows[0]).not.toContain('icon-delete');
    // An icon of the server's template files has no owner and is read-only.
    expect(shown(rows[1])).toBe('valve Server · 3 × 2 pixels, 0.1 KiB Use');
    expect(rows[1]).not.toContain('icon-rename');
    expect(rows[1]).not.toContain('icon-delete');
    expect(iconOwnerText({ owner: 'phenix' })).toBe('Uploaded by phenix');
    expect(iconOwnerText({ owner: '' })).toBe(SERVER_ICON_OWNER);

    // The filter is a labelled field. The other fields are checkboxes.
    expect(html).toMatch(/<label for="icon-filter"[^>]*>Filter icons<\/label>/);
    expect(
      tags(html, 'input').filter((tag) => !tag.includes('type="checkbox"')),
    ).toHaveLength(2);

    // The diagram's copy the server has as it is says so. One it lacks
    // offers to add it.
    const copies = html.slice(
      html.indexOf('data-testid="icon-diagram-list"'),
      html.indexOf('data-testid="icon-library-heading"'),
    );

    expect(copies).toContain('aria-label="Add aaa to the server"');
    expect(copies).toContain('aria-label="Add plc to the server"');
  });

  test('once the library is read, a copy it lacks can be selected, and the copies have a row of their own', async () => {
    const html = await render(
      IconDialog,
      {},
      provides({ library: { status: 'ready', icons: server } }),
    );
    const copies = html.slice(
      html.indexOf('data-testid="icon-diagram-list"'),
      html.indexOf('data-testid="icon-library-heading"'),
    );
    const list = html.slice(html.indexOf('data-testid="icon-library-list"'));
    const rows = (part) =>
      part
        .split('<li')
        .slice(1)
        .map((row) => row.slice(0, row.indexOf('</li>')));
    const [hmi, valve] = rows(list);

    // Both copies, which the server lacks. No server icon has a checkbox.
    expect(copies.match(/data-testid="icon-select"/g)).toHaveLength(2);
    expect(copies).toMatch(
      /<label class="builder-card__select"[^>]*><input type="checkbox"[^>]*data-testid="icon-select"[^>]*><span class="builder-visually-hidden"[^>]*>Select aaa<\/span><\/label>/,
    );
    expect(list).not.toContain('icon-select');

    // A copy's row is named by its icon and whether it is selected.
    expect(copies).toContain(
      'aria-labelledby="icon-name-diagram-0 icon-state-diagram-0"',
    );
    expect(copies).toMatch(
      /<span id="icon-state-diagram-0" hidden[^>]*>not selected<\/span>/,
    );

    // A server icon's row is the list's one Tab stop, the first, named by
    // its icon.
    expect(hmi).toMatch(/^ class="builder-icons__row"[^>]*tabindex="0"/);
    expect(hmi).toContain('aria-labelledby="icon-name-library-0"');
    expect(valve).toContain('tabindex="-1"');
    expect(valve).toContain('aria-labelledby="icon-name-library-1"');

    // Only the copies have a row above them, with its action.
    const add = tags(html, 'button').find((tag) =>
      tag.includes('data-testid="bulk-add-icons-diagram"'),
    );

    expect(html).toContain('aria-label="Bulk actions: In this diagram"');
    expect(html).not.toContain('aria-label="Bulk actions: Server icons"');
    expect(html).not.toContain('bulk-delete-icons-library');
    expect(html).toMatch(
      /data-testid="bulk-count-icons-diagram"[^>]*>\s*0 of 2 selected\s*</,
    );
    expect(add).toContain('aria-describedby="bulk-count-icons-diagram"');
    expect(add).toContain('aria-disabled="true"');
    // Its name stays in a label of its own while it is busy (BuilderBusyButton.vue).
    expect(html.replace(/<!--.*?-->/g, '')).toMatch(
      /data-testid="bulk-add-icons-diagram"[^>]*><span class="builder-busy-button__label"[^>]*>\s*Add selected to server\s*<\/span>/,
    );
    // The row of the copies stands between their hint and their list.
    expect(html.indexOf('bulk-bar-icons-diagram')).toBeLessThan(
      html.indexOf('data-testid="icon-diagram-list"'),
    );
  });

  test('a copy the server has as it is is on the server', async () => {
    const html = await render(
      IconDialog,
      {},
      provides({
        icons: { diagram: () => [{ name: 'HMI', data: ICON_DATA }] },
        library: { status: 'ready', icons: server },
      }),
    );

    expect(textOf(html)).toContain(
      'HMI 1 × 1 pixels, 0.1 KiB Use On the server',
    );
    expect(html).not.toContain('Add HMI to the server');
  });

  test('a diagram without copies has no list of them', async () => {
    const html = await render(
      IconDialog,
      {},
      provides({ icons: { diagram: () => [] } }),
    );

    expect(html).not.toContain('In this diagram');
    expect(html).not.toContain('icon-diagram-list');
    expect(tags(html, 'img')).toEqual([]);
  });

  test('opened where no form provides icons, it has none and no library', async () => {
    const html = await render(IconDialog);

    expect(html).not.toContain('In this diagram');
    expect(textOf(html)).toContain('The icon library is not available here.');
    expect(html).toContain('data-testid="icon-retry"');
  });
});
