// Notes on devices and switches: where a node's notes are (nodeNotes), the
// card below the node (NodeNotes.vue) and the room it takes, which the
// layouts leave and image downloads hold (nodeNotes.js), what the info
// tooltip and the node's description say of them (nodeInfo.js), a switch's
// own notes in the model, the decoder and the Inspector, a device's
// general.notes in the Inspector's checks, the notes of copies, and the
// command that hides them. The validation rules for a switch's notes are in
// the corpus the server shares (validate.test.js); the layouts' room for
// notes in layouts.test.js.

import { readFileSync } from 'node:fs';

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createPinia, setActivePinia } from 'pinia';

// The image downloads' module and the editor's store reach the API client,
// which needs a browser; nothing here sends a request.
vi.mock('@/utils/axios.js', () => ({ default: {} }));

vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice' }),
}));

import { isMultilineList } from '@/components/builder/inspector/control.js';
import NodeNotes from '@/components/builder/nodes/NodeNotes.vue';

import {
  applyFormData,
  inspectorTarget,
  uiSchemaForKind,
} from '@/builder/adapters/forms.js';
import { copySelection, pasteClipboard } from '@/builder/clipboard.js';
import { getCommand } from '@/builder/commands.js';
import { decodeDocument } from '@/builder/decode.js';
import { documentBounds, IMAGE_PADDING } from '@/builder/exporters.js';
import { createFormValidator } from '@/builder/form-validator.js';
import {
  addNode,
  createDocument,
  findNode,
  nodeNotes,
  sizeOf,
  updateNode,
} from '@/builder/model.js';
import { cut, deviceInfo, switchInfo } from '@/builder/nodeInfo.js';
import {
  footprintBounds,
  nodeFootprint,
  noteCharacters,
  NOTE_LINES,
  NOTES_GAP,
  NOTES_SHOWN,
  notesHeight,
  shownNotes,
} from '@/builder/nodeNotes.js';
import { builderSchemaV1, schemaForKind } from '@/builder/schema.js';
import { resetSettings, setSetting } from '@/builder/settings.js';
import { useBuilderStore } from '@/builder/store.js';
import { validateDocument } from '@/builder/validate.js';

afterEach(() => {
  resetSettings(null);
});

// A diagram with a device and a switch, each with the notes given.
function notedDocument({ device = [], network = [] } = {}) {
  const withDevice = addNode(createDocument({ name: 'notes' }), {
    kind: 'device',
    hostname: 'plc-01',
    position: { x: 0, y: 0 },
  });
  const withSwitch = addNode(withDevice.doc, {
    kind: 'switch',
    networkName: 'OT',
    position: { x: 0, y: 400 },
    notes: network,
  });
  const plc = withDevice.node;
  const doc = updateNode(withSwitch.doc, plc.id, {
    device: {
      spec: {
        ...plc.device.spec,
        general: { ...plc.device.spec.general, notes: device },
      },
    },
  });

  return {
    doc,
    device: findNode(doc, plc.id),
    hub: findNode(doc, withSwitch.node.id),
  };
}

async function render(component, props) {
  return renderToString(createSSRApp({ render: () => h(component, props) }));
}

const count = (html, text) => html.split(text).length - 1;

describe('the notes of a node', () => {
  test("a device's are its spec's general.notes, a switch's its own", () => {
    const { device, hub } = notedDocument({
      device: ['Core router.', 7, '  ', 'Reset before\neach run.'],
      network: ['Mirror port 24'],
    });

    // Not text, or only white space, shows nothing.
    expect(nodeNotes(device)).toEqual([
      'Core router.',
      'Reset before\neach run.',
    ]);
    expect(nodeNotes(hub)).toEqual(['Mirror port 24']);
    expect(nodeNotes({ kind: 'note', note: { text: 'Free text' } })).toEqual(
      [],
    );
    expect(nodeNotes(undefined)).toEqual([]);
  });

  test('a switch keeps its notes until they are emptied, as one with none', () => {
    const { doc, hub } = notedDocument({ network: ['Mirror port 24'] });

    expect(hub.switch.notes).toEqual(['Mirror port 24']);

    // A patch that names no notes keeps them.
    const colored = updateNode(doc, hub.id, {
      switch: { outlineColor: '#2f6fbf' },
    });

    expect(findNode(colored, hub.id).switch).toMatchObject({
      outlineColor: '#2f6fbf',
      notes: ['Mirror port 24'],
    });

    const notes = ['First', 'Second'];
    const changed = updateNode(colored, hub.id, { switch: { notes } });

    expect(findNode(changed, hub.id).switch.notes).toEqual(notes);
    expect(findNode(changed, hub.id).switch.notes).not.toBe(notes);

    for (const emptied of [[], undefined, null]) {
      const cleared = updateNode(changed, hub.id, {
        switch: { notes: emptied },
      });

      expect(findNode(cleared, hub.id).switch).not.toHaveProperty('notes');
    }

    // A new switch given no notes has none.
    const plain = addNode(createDocument(), { kind: 'switch' }).node;

    expect(plain.switch).not.toHaveProperty('notes');
  });

  test("the decoder refuses a switch's notes that are no list, as the server does", () => {
    const { doc } = notedDocument({ network: ['Mirror port 24'] });
    const index = doc.nodes.findIndex((node) => node.kind === 'switch');
    const broken = structuredClone(doc);

    expect(() => decodeDocument(structuredClone(doc))).not.toThrow();

    broken.nodes[index].switch.notes = 'one note';
    expect(() => decodeDocument(broken)).toThrow(
      `"nodes[${index}].switch.notes" must be an array`,
    );

    broken.nodes[index].switch.notes = null;
    expect(() => decodeDocument(broken)).not.toThrow();
  });

  test("the diagram checks hold a switch's notes to the diagram's rules, and leave a device's to phenix's schema", () => {
    const { doc } = notedDocument({
      device: ['', 7],
      network: ['kept', ' \t '],
    });
    const index = doc.nodes.findIndex((node) => node.kind === 'switch');
    const issues = validateDocument(doc).filter((issue) =>
      issue.path.includes('notes'),
    );

    expect(issues).toEqual([
      expect.objectContaining({
        path: `nodes[${index}].switch.notes[1]`,
        message: 'note must not be blank',
        level: 'error',
      }),
    ]);
  });
});

describe('the notes card', () => {
  const NOTES = ['One', 'Two\nlines', 'Three', 'Four', 'Five', 'Six', 'Seven'];

  test('shows five notes, then how many more', async () => {
    expect(NOTES_SHOWN).toBe(5);
    expect(shownNotes(NOTES)).toEqual({
      shown: NOTES.slice(0, 5),
      more: 2,
    });
    expect(shownNotes(['One'])).toEqual({ shown: ['One'], more: 0 });

    const html = await render(NodeNotes, { kind: 'switch', notes: NOTES });

    expect(count(html, 'data-testid="node-note"')).toBe(5);
    expect(html).toContain('Two\nlines');
    expect(html).not.toContain('Six');
    expect(html).toContain('+2 more');
    expect(html).toContain('builder-node-notes--switch');
    // The node's description says the notes.
    expect(html).toContain('aria-hidden="true"');
  });

  test('takes the node’s outline color and selection, and goes with the setting or the last note', async () => {
    const html = await render(NodeNotes, {
      kind: 'device',
      notes: ['One'],
      selected: true,
      colorStyle: { '--bx-node-outline': '#2f6fbf' },
    });

    expect(html).toContain('builder-node-notes--device');
    expect(html).toContain('is-selected');
    expect(html).toContain('--bx-node-outline:#2f6fbf');
    expect(html).not.toContain('more');

    expect(
      await render(NodeNotes, { kind: 'device', notes: [] }),
    ).not.toContain('node-notes');

    setSetting('showNodeNotes', false, null);
    expect(
      await render(NodeNotes, { kind: 'device', notes: ['One'] }),
    ).not.toContain('node-notes');
  });

  // nodeNotes.js estimates the card's height from the measures builder.css
  // draws it with.
  test('is drawn with the measures its height is estimated from', () => {
    const css = readFileSync(
      new URL('../../src/builder/builder.css', import.meta.url),
      'utf8',
    );
    const rule = (selector) =>
      css.match(
        new RegExp(`\\n${selector.replace(/[.-]/g, '\\$&')} \\{([^}]*)\\}`),
      )?.[1] || '';

    const card = rule('.builder-node-notes');

    expect(card).toContain('top: calc(100% + 0.5rem);');
    expect(card).toContain('padding: 0.25rem 0.375rem;');
    expect(card).toContain('gap: 0.125rem;');
    expect(card).toContain('border: 1px solid');
    expect(card).toContain('border-left: 3px solid');
    expect(card).toContain('line-height: 0.9375rem;');
    expect(rule('.builder-node-notes--device')).toContain(
      'top: calc(100% + 1rem);',
    );
    expect(rule('.builder-node-notes__note')).toContain(
      `-webkit-line-clamp: ${NOTE_LINES};`,
    );
    expect(NOTES_GAP).toEqual({ device: 16, switch: 8 });
  });
});

describe('the room notes take', () => {
  test('a node without notes takes its box, and one with notes the card below it too', () => {
    const { device, hub } = notedDocument({
      device: ['Core router.'],
      network: ['Mirror port 24'],
    });
    const box = sizeOf(device);

    // The gap, the card's padding and borders, and one line.
    expect(notesHeight(device)).toBe(16 + 10 + 15);
    expect(notesHeight(hub)).toBe(8 + 10 + 15);
    expect(nodeFootprint(device)).toEqual({
      width: box.width,
      height: box.height + 41,
    });
    expect(nodeFootprint(device, { showNotes: false })).toEqual(box);

    const bare = notedDocument().device;

    expect(notesHeight(bare)).toBe(0);
    expect(nodeFootprint(bare)).toEqual(sizeOf(bare));
  });

  test('a note takes at most three lines, and five notes show with a line for the rest', () => {
    const long = 'word '.repeat(60);
    const { device } = notedDocument({ device: [long] });

    expect(notesHeight(device)).toBe(16 + 10 + NOTE_LINES * 15);

    // Seven one-line notes: five, then "+2 more", with 2 pixels between rows.
    const many = notedDocument({
      device: ['a', 'b', 'c', 'd', 'e', 'f', 'g'],
    }).device;

    expect(notesHeight(many)).toBe(16 + 10 + 6 * 15 + 5 * 2);
  });

  // The card wraps at spaces first and breaks a word only when it is longer
  // than a line (white-space: pre-wrap, overflow-wrap: anywhere), so a note
  // of words can take more lines than its characters fill.
  test('a note wraps at its spaces, each word that does not fit starting a line', () => {
    const { device } = notedDocument();
    // The characters of the card's text the estimate fits across a device.
    const perLine = Math.floor((sizeOf(device).width - 16) / 6);
    const heightOf = (note) =>
      notesHeight(notedDocument({ device: [note] }).device);
    const lines = (lineCount) => 16 + 10 + lineCount * 15;
    const medium = 'w'.repeat(Math.floor(perLine / 2) + 1);
    const several = [medium, medium, medium].join(' ');

    // Three medium words fill less than two lines of characters, but no two
    // of them share a line.
    expect(Array.from(several).length).toBeLessThanOrEqual(2 * perLine);
    expect(heightOf(several)).toBe(lines(3));
    expect(heightOf([medium, medium].join(' '))).toBe(lines(2));
    expect(heightOf('Core router')).toBe(lines(1));

    // A word as long as a line fits it; a longer one breaks across the
    // lines it fills, starting a line of its own after a shorter word.
    expect(heightOf('w'.repeat(perLine))).toBe(lines(1));
    expect(heightOf('w'.repeat(perLine + 1))).toBe(lines(2));
    expect(heightOf(`ab ${'w'.repeat(perLine + 1)}`)).toBe(lines(3));
    expect(heightOf(`${'w'.repeat(perLine + 1)} ab`)).toBe(lines(2));

    // A tab counts as four characters.
    expect(heightOf(`${'w'.repeat(perLine - 5)}\tx`)).toBe(lines(1));
    expect(heightOf(`${'w'.repeat(perLine - 4)}\tx`)).toBe(lines(2));
  });

  test('an image holds the notes the canvas shows', () => {
    const { doc, device, hub } = notedDocument({
      network: ['Mirror port 24'],
    });
    const boxes = documentBounds(doc, IMAGE_PADDING, { showNotes: false });
    const withNotes = documentBounds(doc);
    const bottom = hub.position.y + sizeOf(hub).height + notesHeight(hub);

    expect(withNotes.height).toBe(boxes.height + notesHeight(hub));
    expect(withNotes.y + withNotes.height).toBe(bottom + IMAGE_PADDING);
    expect(footprintBounds([device, hub])).toEqual({
      x: 0,
      y: 0,
      width: Math.max(sizeOf(device).width, sizeOf(hub).width),
      height: bottom,
    });
    expect(footprintBounds([])).toEqual({ x: 0, y: 0, width: 0, height: 0 });
  });
});

describe('what a node says of its notes', () => {
  test('a device lists its notes last, in its tooltip and its description', () => {
    const { device } = notedDocument({
      device: ['Core router.', 'Reset its password\nbefore each run'],
    });
    const info = deviceInfo(device);

    expect(info.rows.at(-1)).toEqual({
      label: 'Notes (2)',
      lines: ['Core router.', 'Reset its password before each run'],
    });
    expect(info.text).toMatch(
      / 2 notes: Core router\.; Reset its password before each run\.$/,
    );

    const bare = deviceInfo(notedDocument().device);

    expect(bare.rows.map((row) => row.label)).toEqual([
      'Description',
      'Interfaces',
      'OS type',
    ]);
    expect(bare.text).not.toContain('note');
  });

  test('a switch lists its own: eight in its tooltip, and in its description those its card shows, each then with how many more', () => {
    const notes = Array.from({ length: 10 }, (_, index) => `Note ${index + 1}`);
    const { hub } = notedDocument({ network: notes });
    const network = { name: 'OT' };
    const info = switchInfo(network, [], hub);

    expect(info.rows.at(-1)).toEqual({
      label: 'Notes (10)',
      lines: [...notes.slice(0, 8), '+2 more'],
    });
    // The card shows five notes and "+5 more".
    expect(info.text).toMatch(
      / 10 notes: Note 1; Note 2; Note 3; Note 4; Note 5; and 5 more notes\.$/,
    );

    const six = notedDocument({ network: notes.slice(0, 6) }).hub;

    expect(switchInfo(network, [], six).text).toMatch(
      / 6 notes: Note 1; .*; Note 5; and 1 more note\.$/,
    );
    expect(switchInfo(network, []).text).toBe('No connected devices.');
  });

  // The card is hidden from assistive technology, so the description says
  // at least what the card shows: each note it shows, of up to three lines.
  test('the description says each note the card shows, as much of it as the card can show', () => {
    const words = 'Reset the PLC password before each run '.repeat(8).trim();
    const long = 'x'.repeat(2000);
    const { device } = notedDocument({ device: [words, long] });
    const box = sizeOf(device);
    const longest = noteCharacters(device);
    const info = deviceInfo(device);

    // Three lines across the card at a character a pixel wide, each with
    // the space or line break that ends it: more than the card's lines hold.
    expect(longest).toBe(NOTE_LINES * (box.width - 16 + 1));
    expect(longest).toBeGreaterThan(
      NOTE_LINES * Math.floor((box.width - 16) / 6),
    );

    // A note the card cuts after three lines is said whole; the tooltip cuts
    // it to 80 characters.
    expect(notesHeight(device)).toBe(16 + 10 + 2 * NOTE_LINES * 15 + 2);
    expect(Array.from(words).length).toBeLessThan(longest);
    expect(info.text).toContain(`2 notes: ${words}; `);
    expect(info.rows.at(-1).lines[0]).toBe(cut(words));
    expect(cut(words)).toHaveLength(81);

    // A note longer than the card can show at the node's width is cut past
    // that.
    expect(info.text).toMatch(new RegExp(`; x{${longest}}…$`));

    // A wider node's card shows more of a note, and its description says
    // more.
    const wide = { kind: 'device', size: { width: 400, height: 96 } };

    expect(noteCharacters(wide)).toBe(NOTE_LINES * (400 - 16 + 1));
  });
});

describe('the notes of a copy', () => {
  const DEVICE_NOTES = ['Core router.', 'Reset before\neach run.'];
  const SWITCH_NOTES = ['Mirror port 24', 'Patch panel B'];

  const notesOf = (doc, ids) => ids.map((id) => nodeNotes(findNode(doc, id)));

  test('a pasted device keeps its general.notes, and a pasted switch its own notes', () => {
    const { doc, device, hub } = notedDocument({
      device: DEVICE_NOTES,
      network: SWITCH_NOTES,
    });
    const clipboard = copySelection(doc, { nodes: [device.id, hub.id] });

    // Into the same diagram, and into another, which takes the network too.
    for (const target of [doc, createDocument({ name: 'other' })]) {
      const pasted = pasteClipboard(target, clipboard);

      expect(notesOf(pasted.doc, pasted.nodeIds)).toEqual([
        DEVICE_NOTES,
        SWITCH_NOTES,
      ]);
      const issues = validateDocument(pasted.doc).filter((issue) =>
        issue.path.includes('notes'),
      );

      expect(issues).toEqual([]);
    }

    // A copy's notes are its own: changing them leaves the original's.
    const pasted = pasteClipboard(doc, clipboard);
    const copy = findNode(pasted.doc, pasted.nodeIds[1]);
    const changed = updateNode(pasted.doc, copy.id, {
      switch: { notes: ['Moved to rack 3'] },
    });

    expect(copy.switch.notes).not.toBe(hub.switch.notes);
    expect(nodeNotes(findNode(changed, hub.id))).toEqual(SWITCH_NOTES);

    // A switch without notes is copied without any.
    const bare = notedDocument();
    const plain = pasteClipboard(
      bare.doc,
      copySelection(bare.doc, { nodes: [bare.hub.id] }),
    );

    expect(findNode(plain.doc, plain.nodeIds[0]).switch).not.toHaveProperty(
      'notes',
    );
  });

  test('copy and paste, and duplicate, in the editor keep the notes', () => {
    setActivePinia(createPinia());

    const store = useBuilderStore();
    const { doc, device, hub } = notedDocument({
      device: DEVICE_NOTES,
      network: SWITCH_NOTES,
    });

    store.setDocument(doc);
    store.selection = { nodes: [device.id, hub.id], edges: [] };

    const duplicated = store.duplicate();

    expect(notesOf(store.doc, duplicated)).toEqual([
      DEVICE_NOTES,
      SWITCH_NOTES,
    ]);

    store.selection = { nodes: [hub.id], edges: [] };
    store.copy();

    const pasted = store.paste();

    expect(notesOf(store.doc, pasted)).toEqual([SWITCH_NOTES]);
    expect(notesOf(store.doc, [device.id, hub.id])).toEqual([
      DEVICE_NOTES,
      SWITCH_NOTES,
    ]);
  });
});

describe('notes in the Inspector', () => {
  const ui = (schema) => JSON.stringify(schema);

  test('a switch edits its notes as a device edits its general.notes: one text area each', () => {
    const { properties } = schemaForKind(builderSchemaV1, 'switch');

    expect(properties.notes.type).toBe('array');
    expect(ui(uiSchemaForKind(builderSchemaV1, 'switch'))).toContain(
      '"scope":"#/properties/notes"',
    );
    expect(isMultilineList('notes')).toBe(true);
    expect(isMultilineList('spec.general.notes')).toBe(true);
  });

  test("a switch's notes are applied with its network's fields, in one change, and emptied to none", () => {
    const { doc, hub } = notedDocument({ network: ['Mirror port 24'] });
    const selection = { type: 'node', id: hub.id };
    const target = inspectorTarget(doc, selection);

    expect(target.data.notes).toEqual(['Mirror port 24']);
    expect(target.data.notes).not.toBe(hub.switch.notes);

    const applied = applyFormData(doc, selection, {
      ...target.data,
      description: 'Plant network',
      notes: ['Mirror port 24', 'Patch panel B'],
    });

    expect(findNode(applied, hub.id).switch.notes).toEqual([
      'Mirror port 24',
      'Patch panel B',
    ]);
    expect(applied.networks[0].description).toBe('Plant network');

    // The form leaves no notes once the last is removed.
    const emptied = { ...target.data };

    delete emptied.notes;

    const cleared = applyFormData(doc, selection, emptied);

    expect(findNode(cleared, hub.id).switch).not.toHaveProperty('notes');
    expect(inspectorTarget(cleared, selection).data).not.toHaveProperty(
      'notes',
    );
  });

  // phenix's schema bounds a device's general.notes, which the Inspector
  // checks the spec against; a publish refuses the same notes.
  test("a device's notes that phenix's schema refuses are field errors", () => {
    const { doc, device } = notedDocument();
    const data = inspectorTarget(doc, { type: 'node', id: device.id }).data;
    const validate = createFormValidator().compile(
      schemaForKind(builderSchemaV1, 'device'),
    );
    const notesErrors = (notes) => {
      validate({
        ...data,
        spec: { ...data.spec, general: { ...data.spec.general, notes } },
      });

      return (validate.errors || [])
        .filter((error) => error.instancePath.startsWith('/spec/general/notes'))
        .map((error) => `${error.instancePath} ${error.keyword}`);
    };

    expect(notesErrors(['Core router.', 'Two\nlines'])).toEqual([]);
    expect(notesErrors(['kept', 7])).toContain('/spec/general/notes/1 type');
    expect(notesErrors(['kept', ''])).toContain(
      '/spec/general/notes/1 minLength',
    );
    expect(notesErrors(Array(101).fill('note'))).toContain(
      '/spec/general/notes maxItems',
    );
    expect(notesErrors('one note')).toContain('/spec/general/notes type');
  });

  test("a switch's notes are held to the diagram's rules", () => {
    const { doc, hub } = notedDocument();
    const data = inspectorTarget(doc, { type: 'node', id: hub.id }).data;
    const validate = createFormValidator().compile(
      schemaForKind(builderSchemaV1, 'switch'),
    );
    const notesErrors = (notes) => {
      validate({ ...data, notes });

      return (validate.errors || [])
        .filter((error) => error.instancePath.startsWith('/notes'))
        .map((error) => `${error.instancePath} ${error.keyword}`);
    };

    expect(notesErrors(['Mirror port 24', 'Two\n\tlines'])).toEqual([]);
    expect(notesErrors([''])).toContain('/notes/0 minLength');
    expect(notesErrors(['ring\u0007'])).toContain('/notes/0 pattern');
    expect(notesErrors(Array(101).fill('note'))).toContain('/notes maxItems');
  });
});

describe('the Show or hide node notes command', () => {
  test('says what it does and asks the view to do it', () => {
    const command = getCommand('view.nodeNotes');
    const view = { showNodeNotes: true, toggleNodeNotes: vi.fn() };

    expect(command.title).toBe('Show or hide node notes');
    expect(command.group).toBe('View');
    expect(command.label({ view })).toBe('Hide node notes');
    expect(command.label({ view: { showNodeNotes: false } })).toBe(
      'Show node notes',
    );

    command.run({ view });
    expect(view.toggleNodeNotes).toHaveBeenCalledOnce();
  });
});
