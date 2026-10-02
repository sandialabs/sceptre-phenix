// Where a custom icon is drawn: the canvas nodes, the outline rows and the
// Custom icons dialog, each through BuilderIcon, as an <img> with a PNG
// data URL. The dialog's requests and its file chooser need a browser, so
// they are left to the Playwright specs.

// Nothing here stands in for the API client: neither BuilderIcon, the
// nodes, the outline rows nor the dialog import it. The dialog is given its
// library by the Inspector (see iconLibrary.js).

import { describe, expect, test } from 'vitest';
import { createSSRApp, h, reactive } from 'vue';
import { renderToString } from 'vue/server-renderer';

import BuilderIcon from '@/components/builder/BuilderIcon.vue';
import BuilderOutlineList from '@/components/builder/BuilderOutlineList.vue';
import IconDialog from '@/components/builder/dialogs/IconDialog.vue';
import {
  INSPECTOR_ICON_LIBRARY,
  INSPECTOR_ICONS,
} from '@/components/builder/inspector/control.js';
import GroupNode from '@/components/builder/nodes/GroupNode.vue';

import { toFlowNodes } from '@/builder/adapters/vueflow.js';
import { nodeIcon } from '@/builder/catalog.js';
import { iconId, iconSrc } from '@/builder/icons.js';
import { addNode, updateNode } from '@/builder/model.js';
import { buildOutline } from '@/builder/outline.js';
import { keepUnchanged } from '@/builder/stable.js';

import { sampleDocument, tags } from './fixtures.js';
import { base64Of, ICON_DATA, ICON_KEY, png } from './png.js';

const SRC = `data:image/png;base64,${ICON_DATA}`;
const PLC = { name: 'plc', data: ICON_DATA };

async function render(component, props = {}, provides = []) {
  const app = createSSRApp({ render: () => h(component, props) });

  for (const [key, value] of provides) {
    app.provide(key, value);
  }

  return renderToString(app);
}

// What an element holds, as text: tags and comments out, white space as
// one space.
function textOf(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]*>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

// The sample document with a custom icon on its alpha device and on a
// group, which carries it.
function iconDocument() {
  const sample = sampleDocument();
  const group = addNode(sample.doc, {
    kind: 'group',
    title: 'Zone',
    icon: ICON_KEY,
  });

  return {
    ...sample,
    group: group.node,
    doc: {
      ...updateNode(group.doc, sample.alpha.id, { device: { icon: ICON_KEY } }),
      icons: { [ICON_KEY]: PLC },
    },
  };
}

describe('the custom icon a node names', () => {
  test('is a device’s or a group’s, and only an icon id', () => {
    expect(nodeIcon({ kind: 'device', device: { icon: ICON_KEY } })).toBe(
      ICON_KEY,
    );
    expect(nodeIcon({ kind: 'group', group: { icon: ICON_KEY } })).toBe(
      ICON_KEY,
    );

    for (const node of [
      undefined,
      { kind: 'device', device: {} },
      { kind: 'device', device: { icon: '' } },
      { kind: 'device', device: { icon: 'router' } },
      { kind: 'device', device: { icon: 'https://example.com/a.png' } },
      { kind: 'device', device: { icon: 'data:image/svg+xml;base64,AAAA' } },
      { kind: 'device', device: { icon: 7 } },
      { kind: 'group', group: { iconKey: 'firewall' } },
      // A switch and a note have none.
      { kind: 'switch', switch: { icon: ICON_KEY } },
      { kind: 'note', note: { icon: ICON_KEY } },
    ]) {
      expect(nodeIcon(node), JSON.stringify(node)).toBe('');
    }
  });

  test('an outline row has it, beside the icon of its key', () => {
    const { doc, alpha, bravo, sw, group } = iconDocument();
    const rows = Object.fromEntries(
      buildOutline(doc).map((item) => [item.id, item]),
    );

    expect(rows[alpha.id]).toMatchObject({ iconKey: 'linux', icon: ICON_KEY });
    expect(rows[group.id]).toMatchObject({
      iconKey: 'container',
      icon: ICON_KEY,
    });
    expect(rows[bravo.id].icon).toBe('');
    expect(rows[sw.id].icon).toBe('');
  });

  test('a flow node has the address it is drawn from, or none', () => {
    const { doc, alpha, bravo, sw, group } = iconDocument();
    const data = (document, id) =>
      toFlowNodes(document).find((node) => node.id === id).data;

    expect(data(doc, alpha.id)).toMatchObject({
      iconKey: 'linux',
      iconSrc: SRC,
    });
    expect(data(doc, group.id).iconSrc).toBe(SRC);
    expect(data(doc, bravo.id).iconSrc).toBe('');
    expect(data(doc, sw.id).iconSrc).toBe('');

    // A document that names an icon it does not carry draws the built-in
    // one: nothing is fetched for it.
    const bare = { ...doc };

    delete bare.icons;
    expect(data(bare, alpha.id).iconSrc).toBe('');
    expect(data(bare, alpha.id).iconKey).toBe('linux');
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
    const id = iconId(other);
    const swapped = {
      ...updateNode(doc, alpha.id, { device: { icon: id } }),
      icons: { ...doc.icons, [id]: { data: base64Of(other) } },
    };
    const next = keepUnchanged(toFlowNodes(swapped), before);

    expect(at(next, alpha.id).data.iconSrc).toBe(
      `data:image/png;base64,${base64Of(other)}`,
    );
    expect(at(next, bravo.id)).toBe(at(before, bravo.id));
  });
});

describe('BuilderIcon', () => {
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

  test('without an address it is the built-in icon it always was', async () => {
    const html = await render(BuilderIcon, { name: 'check', title: 'Done' });

    expect(html).toContain('role="img"');
    expect(html).toContain('aria-label="Done"');
    expect(html).toMatch(/<title[^>]*>Done<\/title>/);
    expect(html).toContain('<path d="M5 13l4 4 10-10"');
    expect(tags(html, 'img')).toEqual([]);
  });
});

describe('a custom icon on the canvas and in the outline', () => {
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

  test('an outline row draws it, and other rows the icon of their key', async () => {
    const { doc, alpha, bravo } = iconDocument();
    const outline = reactive({
      rowId: (id) => `outline-row-${id}`,
      iconFor: (item) => item.iconKey,
      iconSrcFor: (item) => iconSrc(item.icon, doc.icons),
      isPressed: () => false,
      isRoving: () => false,
      isRenaming: () => false,
    });
    const html = await render(
      BuilderOutlineList,
      { items: buildOutline(doc) },
      [['builderOutline', outline]],
    );
    const row = (id) =>
      html.slice(
        html.indexOf(`data-testid="outline-item-${id}"`),
        html.indexOf('</button>', html.indexOf(`outline-item-${id}`)),
      );

    expect(tags(row(alpha.id), 'img')).toHaveLength(1);
    expect(row(alpha.id)).toContain(`src="${SRC}"`);
    expect(row(alpha.id)).toContain('width="14"');
    expect(tags(row(bravo.id), 'img')).toEqual([]);
    expect(row(bravo.id)).toContain('builder-icon--linux');
    // The row's name is its text, not its icon.
    expect(row(alpha.id)).toContain('aria-label="Device alpha');
  });
});

describe('the Custom icons dialog', () => {
  const other = png(3, 2, [9, 9, 9, 255]);
  const diagram = [
    { id: iconId(other), name: '', data: base64Of(other) },
    { id: ICON_KEY, name: 'plc <b>', data: ICON_DATA },
  ];
  const provides = (extra = {}) => [
    [
      INSPECTOR_ICONS,
      {
        entry: () => undefined,
        shelve: () => {},
        diagram: () => diagram,
        full: () => false,
        ...extra,
      },
    ],
    [
      INSPECTOR_ICON_LIBRARY,
      {
        list: () => new Promise(() => {}),
        upload: () => new Promise(() => {}),
        remove: () => new Promise(() => {}),
        failure: () => 'failed',
      },
    ],
  ];

  test('says what an icon is, and has one button to upload a file', async () => {
    const html = await render(IconDialog, {}, provides());

    expect(html).toMatch(/<h2 id="icon-dialog-title"[^>]*>Custom icons<\/h2>/);
    expect(html).toContain('aria-labelledby="icon-dialog-title"');
    expect(textOf(html)).toContain(
      'An icon is a small image, drawn at 16 pixels. PNG, JPEG, GIF, WebP and SVG files are converted to a PNG of at most 96 by 96 pixels.',
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
      /<p class="builder-dialog__message builder-dialog__error" role="alert"[^>]*><!----><\/p>/,
    );
    expect(textOf(html)).toMatch(/Close$/);
  });

  test('lists the diagram’s icons by name, each with its size and Use', async () => {
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
    // An icon without a name comes first, and is named as such.
    expect(textOf(`<li${rows[0]}`)).toBe(
      'Unnamed icon 3 × 2 pixels, 0.1 KiB Use',
    );
    expect(rows[0]).toContain('aria-label="Use Unnamed icon"');
    expect(textOf(`<li${rows[1]}`)).toBe(
      'plc &lt;b&gt; 1 × 1 pixels, 0.1 KiB Use',
    );
    expect(rows[1]).toContain('aria-label="Use plc &lt;b&gt;"');

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

    // Whether the library has an icon is not known until it is read.
    expect(list).not.toContain('Save to my library');
    expect(list).not.toContain('In my library');
    expect(textOf(html)).toContain('My library Loading your library…');
  });

  test('a diagram without icons has no list of them', async () => {
    const html = await render(IconDialog, {}, provides({ diagram: () => [] }));

    expect(html).not.toContain('In this diagram');
    expect(html).not.toContain('icon-diagram-list');
    expect(tags(html, 'img')).toEqual([]);
  });

  test('opened where no form provides icons, it has none and no library', async () => {
    const html = await render(IconDialog);

    expect(html).not.toContain('In this diagram');
    expect(textOf(html)).toContain('My library Loading your library…');
  });
});
