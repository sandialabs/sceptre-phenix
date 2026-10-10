// Where a custom icon is drawn: the canvas nodes, the outline rows and the
// Custom icons dialog, each through BuilderIcon, as an <img> with a PNG
// data URL. The dialog's requests and its file chooser need a browser, so
// they are left to the Playwright specs.

// Nothing here reaches the server: the flow nodes are given an icon library
// of the test's own, or the Builder's, which nothing here reads, and the
// dialog is given its library as the Inspector gives it (see
// iconLibrary.js).

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
import { SERVER_ICON_OWNER, iconOwnerText, iconSrc } from '@/builder/icons.js';
import { addNode, updateNode } from '@/builder/model.js';
import { buildOutline } from '@/builder/outline.js';
import { keepUnchanged } from '@/builder/stable.js';

import { sampleDocument, tags } from './fixtures.js';
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

  test('an outline row has it, beside the icon of its key', () => {
    const { doc, alpha, bravo, sw, group } = iconDocument();
    const rows = Object.fromEntries(
      buildOutline(doc).map((item) => [item.id, item]),
    );

    expect(rows[alpha.id]).toMatchObject({ iconKey: 'linux', icon: 'plc' });
    expect(rows[group.id]).toMatchObject({
      iconKey: 'container',
      icon: 'plc',
    });
    expect(rows[bravo.id].icon).toBe('');
    expect(rows[sw.id].icon).toBe('');
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

  // The external device is a monitor in dashes, with a stand. The box
  // with an arrow out of it is the mark of a link that opens a new tab.
  test('external is a dashed monitor, and external-link the new-tab mark', async () => {
    const external = await render(BuilderIcon, { name: 'external' });
    const link = await render(BuilderIcon, { name: 'external-link' });
    const arrow = '<path d="M17 7h4v4M21 7l-6 6"/>';

    expect(external).toContain('class="builder-icon builder-icon--external"');
    // The screen is in parts, a dash each, and the stand is whole.
    const [screen, stand] = [...external.matchAll(/<path d="([^"]*)"/g)].map(
      ([, d]) => d,
    );

    expect(screen.match(/M/g)).toHaveLength(8);
    expect(stand).toBe('M12 16v4M8 20h8');
    expect(external).not.toContain(arrow);

    expect(link).toContain('class="builder-icon builder-icon--external-link"');
    expect(link).toContain(arrow);
    // Not the server icon an unknown name falls back to.
    expect(link).not.toContain('<circle');
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
      owner: 'bob',
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
        maxIcons: 64,
        maxBytes: 1048576,
        usedIcons: 0,
        usedBytes: 0,
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
      remove: pending,
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
      'An icon is a small image that every user of this server can use, drawn at 16, 24 or 32 pixels as the node&#39;s icon size says. PNG, JPEG, GIF, WebP and SVG files are converted to a PNG of at most 96 by 96 pixels.',
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

  test('lists the server’s icons, with Rename and Delete only where the user may', async () => {
    const html = await render(
      IconDialog,
      {},
      provides({
        library: {
          status: 'ready',
          icons: server,
          usedIcons: 1,
          usedBytes: 70,
        },
      }),
    );
    const list = html.slice(html.indexOf('data-testid="icon-library-list"'));
    const rows = list
      .split('<li')
      .slice(1)
      .map((row) => row.slice(0, row.indexOf('</li>')));

    // A row's text, without its checkbox and the hidden words that say
    // whether it is selected (see the test of selecting below).
    const shown = (row) =>
      textOf(
        `<li${row}`
          .replace(/<label\b[\s\S]*?<\/label>/, '')
          .replace(/<span id="icon-state-[^"]*"[^>]*>[^<]*<\/span>/, ''),
      );

    expect(textOf(html)).toContain('Server icons (2)');
    expect(textOf(html)).toContain(
      'You uploaded 1 of 64 icons, 0.1 KiB of 1 MiB.',
    );
    expect(shown(rows[0])).toBe(
      'hmi Uploaded by alice · 1 × 1 pixels, 0.1 KiB · also named old-hmi Use Rename Delete',
    );
    expect(rows[0]).toContain('aria-label="Rename hmi"');
    expect(rows[0]).toContain('aria-label="Delete hmi from the server"');
    expect(shown(rows[1])).toBe(
      'valve Uploaded by bob · 3 × 2 pixels, 0.1 KiB Use',
    );

    // The filter is a labelled field; the other fields are checkboxes.
    expect(html).toMatch(/<label for="icon-filter"[^>]*>Filter icons<\/label>/);
    expect(
      tags(html, 'input').filter((tag) => !tag.includes('type="checkbox"')),
    ).toHaveLength(2);

    // The diagram's copy the server has as it is says so; one it lacks
    // offers to add it.
    const copies = html.slice(
      html.indexOf('data-testid="icon-diagram-list"'),
      html.indexOf('data-testid="icon-library-heading"'),
    );

    expect(copies).toContain('aria-label="Add aaa to the server"');
    expect(copies).toContain('aria-label="Add plc to the server"');
  });

  test('once the library is read, a copy it lacks and an icon the user may delete can be selected, and each list has a row of its own', async () => {
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

    // Both copies, which the server lacks; of the server's icons only hmi,
    // which the user may delete.
    expect(copies.match(/data-testid="icon-select"/g)).toHaveLength(2);
    expect(hmi).toMatch(
      /<label class="builder-card__select"[^>]*><input type="checkbox"[^>]*data-testid="icon-select"[^>]*><span class="builder-visually-hidden"[^>]*>Select hmi<\/span><\/label>/,
    );
    expect(valve).not.toContain('icon-select');

    // A row is the list's one Tab stop, the first, named by its icon and
    // whether it is selected; one that cannot be selected by its icon.
    expect(hmi).toMatch(/^ class="builder-icons__row"[^>]*tabindex="0"/);
    expect(hmi).toContain(
      'aria-labelledby="icon-name-library-0 icon-state-library-0"',
    );
    expect(hmi).toMatch(
      /<span id="icon-state-library-0" hidden[^>]*>not selected<\/span>/,
    );
    expect(valve).toContain('tabindex="-1"');
    expect(valve).toContain('aria-labelledby="icon-name-library-1"');

    // The rows above the lists, each with its action.
    const add = tags(html, 'button').find((tag) =>
      tag.includes('data-testid="bulk-add-icons-diagram"'),
    );
    const remove = tags(html, 'button').find((tag) =>
      tag.includes('data-testid="bulk-delete-icons-library"'),
    );

    expect(html).toContain('aria-label="Bulk actions: In this diagram"');
    expect(html).toContain('aria-label="Bulk actions: Server icons"');
    expect(html).toMatch(
      /data-testid="bulk-count-icons-diagram"[^>]*>\s*0 of 2 selected\s*</,
    );
    expect(html).toMatch(
      /data-testid="bulk-count-icons-library"[^>]*>\s*0 of 1 selected\s*</,
    );
    expect(add).toContain('aria-describedby="bulk-count-icons-diagram"');
    expect(add).toContain('aria-disabled="true"');
    expect(remove).toContain('aria-describedby="bulk-count-icons-library"');
    expect(remove).toContain('aria-haspopup="dialog"');
    expect(remove).toContain('aria-disabled="true"');
    expect(html).toMatch(
      /data-testid="bulk-add-icons-diagram"[^>]*>\s*Add selected to server\s*<\/button>/,
    );
    // The row of the copies stands between their hint and their list.
    expect(html.indexOf('bulk-bar-icons-diagram')).toBeLessThan(
      html.indexOf('data-testid="icon-diagram-list"'),
    );
  });

  test('an icon the server added from its template files is listed as the server’s', async () => {
    // As the server lists it to an account named phenix without the
    // builder-icons permissions: no owner, and neither Rename nor Delete.
    const html = await render(
      IconDialog,
      {},
      provides({
        library: {
          status: 'ready',
          icons: [
            {
              name: 'rtu',
              owner: '',
              width: 1,
              height: 1,
              bytes: 70,
              aliases: [],
              data: ICON_DATA,
              canRename: false,
              canDelete: false,
            },
          ],
        },
      }),
    );
    const list = html.slice(html.indexOf('data-testid="icon-library-list"'));
    const rows = list
      .split('<li')
      .slice(1)
      .map((row) => row.slice(0, row.indexOf('</li>')));

    expect(textOf(`<li${rows[0]}`)).toBe(
      'rtu Server · 1 × 1 pixels, 0.1 KiB Use',
    );
    expect(rows[0]).not.toContain('icon-rename');
    expect(rows[0]).not.toContain('icon-delete');
    expect(iconOwnerText({ owner: 'phenix' })).toBe('Uploaded by phenix');
    expect(iconOwnerText({ owner: '' })).toBe(SERVER_ICON_OWNER);
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
