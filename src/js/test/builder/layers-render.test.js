// The layers editor (LayersDialog.vue) and the drafts page's Layers tab
// (BuilderLayerSets.vue), rendered on the server. Editing is left to e2e.

import { describe, expect, test } from 'vitest';

import BuilderLayerSets from '@/components/builder/BuilderLayerSets.vue';
import LayersDialog from '@/components/builder/dialogs/LayersDialog.vue';

import { attribute, sampleDocument, tagOf, tags, textOf } from './fixtures.js';
import { renderWithStore } from './render.js';

const PURDUE = {
  id: 'builtin:purdue',
  name: 'Purdue Model',
  description: 'The levels of the Purdue model.',
  iconKey: '',
  icon: '',
  matchConflict: 'lowest',
  layerOutlines: false,
  builtin: true,
  version: 1,
  layers: [
    { id: 6, label: 'L5 Enterprise', iconKey: 'server', color: '#2f6fbf' },
    { id: 0, label: 'L0 Process', iconKey: 'transmission', color: '#dc2626' },
  ],
};

const PLANT = {
  id: 'plant',
  name: 'Plant',
  description: '',
  iconKey: 'plc',
  icon: '',
  matchConflict: 'highest',
  layerOutlines: true,
  builtin: false,
  version: 3,
  updated: '2026-01-02T03:04:05Z',
  updatedBy: 'bob',
  layers: [
    { id: 2, label: 'Control', matchers: { 'general.hostname': '^plc-' } },
    { id: 1, label: 'Field' },
  ],
};

async function render(
  component,
  { props, doc, sets, defaultId = null, readOnly = false } = {},
) {
  const { html } = await renderWithStore(component, {
    props,
    prepare(store) {
      store.doc = doc ?? sampleDocument().doc;
      store.readOnly = readOnly;
      store.layerSets = {
        ...store.layerSets,
        status: 'ready',
        loaded: sets !== undefined,
        items: sets ?? [],
        defaultId,
      };
    },
  });

  return html;
}

describe('the layers editor of a diagram', () => {
  test('shows the placeholder layers, each a row with an ID, an icon, a label and Remove', async () => {
    const html = await render(LayersDialog, { sets: [PURDUE] });
    const rows = html.match(/data-testid="layer-row"/g) || [];

    expect(textOf(html)).toContain('Layers of this diagram');
    expect(tagOf(html, 'layers-placeholders')).toBeTruthy();
    expect(rows).toHaveLength(7);
    expect(attribute(tagOf(html, 'layer-id'), 'aria-label')).toBe('Layer 6 ID');
    expect(attribute(tagOf(html, 'layer-label'), 'aria-label')).toBe(
      'Layer 6 label',
    );
    expect(attribute(tagOf(html, 'layer-icon'), 'aria-label')).toBe(
      'Icon of layer 6: No icon',
    );
    expect(attribute(tagOf(html, 'layer-color'), 'aria-label')).toBe(
      'Color of layer 6',
    );
    expect(attribute(tagOf(html, 'layer-remove'), 'aria-label')).toBe(
      'Remove layer 6 – Layer 6',
    );
    expect(textOf(html)).toContain(
      'These layers are not linked to a layer set.',
    );
    // The label is described by how many nodes are at the layer.
    expect(attribute(tagOf(html, 'layer-label'), 'aria-describedby')).toMatch(
      /-uses$/,
    );
    // Its status region is there, empty, from the start.
    expect(html).toMatch(
      /<p[^>]*role="status"[^>]*data-testid="layers-status"[^>]*>(<!--[\s\S]*?-->)*<\/p>/,
    );
  });

  test('says how the copy stands to its set, and offers what fits', async () => {
    const doc = {
      ...sampleDocument().doc,
      layers: PLANT.layers,
      layerSet: {
        name: 'Plant',
        matchConflict: 'highest',
        layerOutlines: true,
        source: { id: 'plant', version: 2 },
      },
    };
    const update = await render(LayersDialog, { doc, sets: [PLANT] });

    expect(textOf(update)).toContain(
      'Layer set Plant has a newer version, 3. This diagram has a copy of version 2.',
    );
    expect(tagOf(update, 'layers-update')).toBeTruthy();
    expect(tagOf(update, 'layers-unlink')).toBeTruthy();
    expect(tagOf(update, 'layers-save-to-set')).toBeFalsy();

    const changed = await render(LayersDialog, {
      doc: {
        ...doc,
        layerSet: { ...doc.layerSet, source: { id: 'plant', version: 3 } },
        layers: [{ id: 2, label: 'Changed' }],
      },
      sets: [PLANT],
    });

    expect(textOf(changed)).toContain(
      'Linked to layer set Plant, version 3. You changed the layers in this diagram after the copy.',
    );
    expect(tagOf(changed, 'layers-save-to-set')).toBeTruthy();
    expect(tagOf(changed, 'layers-update')).toBeFalsy();
  });

  test('shows the options and the node matchers of the diagram', async () => {
    const doc = {
      ...sampleDocument().doc,
      layers: PLANT.layers,
      layerSet: { matchConflict: 'highest', layerOutlines: true },
    };
    const html = await render(LayersDialog, { doc, sets: [PLANT] });
    const conflict = html.match(
      /<select[^>]*data-testid="layers-conflict"[\s\S]*?<\/select>/,
    )[0];

    expect(attribute(tagOf(html, 'layers-outlines'), 'aria-checked')).toBe(
      'true',
    );
    expect(conflict).toMatch(/<select[^>]*value="highest"/);
    expect(attribute(tagOf(html, 'layer-matcher-path'), 'aria-label')).toBe(
      'Field of node matcher 1 of layer 2',
    );
    expect(attribute(tagOf(html, 'layer-matcher-path'), 'value')).toBe(
      'general.hostname',
    );
    expect(attribute(tagOf(html, 'layer-matcher-pattern'), 'value')).toBe(
      '^plc-',
    );
    expect(
      tags(html, 'button').filter((tag) =>
        tag.includes('data-testid="layer-matcher-add"'),
      ),
    ).toHaveLength(2);
    expect(
      tags(html, 'button').find((tag) => tag.includes('layer-matcher-add')),
    ).toContain('aria-label="Add node matcher to layer 2"');
  });

  test('is read only in a read-only draft', async () => {
    const html = await render(LayersDialog, { readOnly: true });

    expect(tagOf(html, 'layers-read-only')).toBeTruthy();
    expect(html).not.toContain('data-testid="layer-remove"');
    expect(html).not.toContain('data-testid="layer-add"');
    expect(html).not.toContain('data-testid="layers-apply"');
    expect(tagOf(html, 'layer-label')).toContain('readonly');
  });
});

describe('the layers editor of a layer set', () => {
  test('a built-in set is read only, with Close and no Save', async () => {
    const html = await render(LayersDialog, {
      props: { mode: 'library', setId: PURDUE.id },
      sets: [PURDUE],
    });

    expect(textOf(html)).toContain('Layer set Purdue Model');
    expect(tagOf(html, 'layers-builtin')).toBeTruthy();
    expect(tagOf(html, 'layers-set-name')).toContain('readonly');
    expect(tagOf(html, 'layers-save')).toBeFalsy();
    expect(textOf(html)).toContain('Close');
    expect(html).not.toContain('data-testid="layer-remove"');
    expect(html.match(/data-testid="layer-row"/g)).toHaveLength(2);
  });

  test('a set shows its name, icon and options, with Cancel and Save', async () => {
    const html = await render(LayersDialog, {
      props: { mode: 'library', setId: PLANT.id },
      sets: [PLANT],
    });

    expect(textOf(html)).toContain('Edit layer set Plant');
    expect(attribute(tagOf(html, 'layers-set-name'), 'value')).toBe('Plant');
    expect(attribute(tagOf(html, 'layers-set-icon'), 'aria-label')).toBe(
      'Icon of the layer set: PLC',
    );
    expect(attribute(tagOf(html, 'layers-outlines'), 'aria-checked')).toBe(
      'true',
    );
    expect(tagOf(html, 'layers-set-name')).not.toContain('readonly');
    expect(tagOf(html, 'layers-save')).toBeTruthy();
    expect(tagOf(html, 'layers-cancel')).toBeTruthy();
    expect(html).toContain('data-testid="layer-remove"');
    // No link part: that is the diagram's.
    expect(html).not.toContain('data-testid="layers-link"');
  });

  test('a new set starts with the placeholder layers', async () => {
    const html = await render(LayersDialog, {
      props: { mode: 'library' },
      sets: [],
    });

    expect(textOf(html)).toContain('New layer set');
    expect(html.match(/data-testid="layer-row"/g)).toHaveLength(7);
    expect(attribute(tagOf(html, 'layers-set-name'), 'value') || '').toBe('');
  });
});

describe('the Layers tab', () => {
  // A card, from its <li> to the end of its buttons.
  const cardOf = (html, id) =>
    html.match(
      new RegExp(
        `<li[^>]*data-testid="layer-set-card-${id}"[\\s\\S]*?class="builder-card__actions[\\s\\S]*?</div>`,
      ),
    )[0];
  // The button of a card's action, by its test id.
  const buttonOf = (card, testid) =>
    card.match(
      new RegExp(`<button[^>]*data-testid="${testid}"[\\s\\S]*?</button>`),
    )[0];

  test('lists each set with its tags, layers and actions', async () => {
    const html = await render(BuilderLayerSets, {
      sets: [PURDUE, PLANT],
      defaultId: 'plant',
    });
    const purdue = cardOf(html, 'builtin:purdue');
    const plant = cardOf(html, 'plant');

    expect(textOf(purdue)).toContain('Purdue Model Built-in');
    expect(textOf(purdue)).toContain('2 layers. Read only.');
    expect(textOf(purdue)).toContain('L5 Enterprise L0 Process');
    expect(textOf(purdue)).not.toContain('6 – ');
    expect(purdue).toContain('aria-label="View Purdue Model"');
    expect(purdue).not.toContain('layer-set-delete');
    expect(purdue).toContain('aria-label="Set default: Purdue Model"');

    expect(textOf(plant)).toContain('Plant Default');
    expect(textOf(plant)).toContain('Changed by bob on');
    expect(plant).toContain('aria-label="Edit Plant"');
    expect(plant).toContain('aria-label="Delete Plant"');
    expect(plant).toContain('aria-label="Clear default: Plant"');
    expect(plant).toContain('builder-icon--plc');

    // Every control is enabled, and nothing describes them.
    const disabled = (tag) => attribute(tag, 'aria-disabled') === 'true';

    for (const testid of ['layer-sets-new', 'layer-sets-upload']) {
      expect(disabled(tagOf(html, testid)), testid).toBe(false);
    }
    expect(
      attribute(tagOf(html, 'layer-sets-upload'), 'aria-describedby'),
    ).toBeNull();
    for (const testid of [
      'layer-set-edit',
      'layer-set-duplicate',
      'layer-set-download',
      'layer-set-default-toggle',
      'layer-set-delete',
    ]) {
      const tag = tagOf(plant, testid);

      expect(disabled(tag), testid).toBe(false);
      expect(attribute(tag, 'aria-describedby'), testid).toBeNull();
    }
  });

  test('the card buttons are the item actions, and their labels stay on one line', async () => {
    const html = await render(BuilderLayerSets, {
      sets: [PURDUE, PLANT],
      defaultId: 'plant',
    });
    const purdue = cardOf(html, 'builtin:purdue');
    const plant = cardOf(html, 'plant');
    const look = (card, testid) => {
      const button = buttonOf(card, testid);

      return {
        text: textOf(button),
        icon: button.match(/builder-icon--([a-z-]+)/)?.[1] ?? null,
        blue: button.includes('builder-button--primary'),
        red: button.includes('builder-button--danger'),
      };
    };

    // Edit is blue with a pencil, and a read-only set has View with an eye.
    expect(look(plant, 'layer-set-edit')).toEqual({
      text: 'Edit',
      icon: 'pencil',
      blue: true,
      red: false,
    });
    expect(look(purdue, 'layer-set-edit')).toEqual({
      text: 'View',
      icon: 'eye',
      blue: false,
      red: false,
    });
    expect(look(plant, 'layer-set-duplicate')).toEqual({
      text: 'Duplicate',
      icon: 'copy',
      blue: false,
      red: false,
    });
    expect(look(plant, 'layer-set-download')).toEqual({
      text: 'Download',
      icon: 'download',
      blue: false,
      red: false,
    });
    expect(look(plant, 'layer-set-delete')).toEqual({
      text: 'Delete',
      icon: 'trash',
      blue: false,
      red: true,
    });

    // The two-column grid of every card, with the default toggle last.
    for (const card of [purdue, plant]) {
      expect(card).toContain('class="builder-card__actions"');
      expect(card).not.toContain('builder-card__actions--fit');
      const buttons = [
        ...card.matchAll(/<button[^>]*data-testid="([a-z-]+)"/g),
      ].map(([, id]) => id);

      expect(buttons.at(-1)).toBe('layer-set-default-toggle');
    }

    // Set default and Clear default: a star, then the label in use. The
    // other is hidden, and the button's name is its aria-label.
    const toggle = buttonOf(plant, 'layer-set-default-toggle');

    expect(look(plant, 'layer-set-default-toggle')).toEqual({
      text: 'Set default Clear default',
      icon: 'star',
      blue: false,
      red: false,
    });
    expect(toggle).toContain('builder-button__swap');
    expect(toggle).toMatch(/<span class="is-off"[^>]*>Set default<\/span>/);
    expect(toggle).toMatch(/<span class=""[^>]*>Clear default<\/span>/);
  });

  test('has no upload hint and no note on new blank diagrams, and a filter and a row for the selection', async () => {
    const html = await render(BuilderLayerSets, {
      sets: [PURDUE, PLANT],
    });

    expect(html).not.toContain('layer-sets-upload-hint');
    expect(textOf(html)).not.toContain('as Download saves it');
    expect(textOf(html)).not.toContain('New blank diagrams copy');
    expect(textOf(html)).toContain(
      'A layer set is a list of layers that every user of this server can use. In a diagram, Edit layers… applies a copy of a set. A layer can have node matchers, which put devices at the layer by their fields.',
    );

    expect(html).toContain('<label for="layer-sets-filter"');
    expect(textOf(html)).toContain('Filter layer sets');
    expect(
      attribute(tagOf(html, 'layer-sets-filter'), 'aria-describedby'),
    ).toBe('layer-sets-filter-count');
    expect(
      textOf(
        html.match(/data-testid="bulk-count-layer-sets"[^>]*>([^<]*)</)[1],
      ),
    ).toBe('0 of 2 selected');

    for (const testid of [
      'bulk-download-layer-sets',
      'bulk-delete-layer-sets',
    ]) {
      const tag = tagOf(html, testid);

      expect(attribute(tag, 'aria-disabled'), testid).toBe('true');
      expect(attribute(tag, 'aria-describedby'), testid).toBe(
        'bulk-count-layer-sets',
      );
    }
  });

  test('says it is loading until the library is read', async () => {
    const html = await render(BuilderLayerSets);

    expect(tagOf(html, 'layer-sets-loading')).toBeTruthy();
    expect(tagOf(html, 'layer-sets-new')).toBeTruthy();
    expect(tagOf(html, 'layer-sets-upload')).toBeTruthy();
  });
});
