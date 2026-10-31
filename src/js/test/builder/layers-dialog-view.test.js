// The description of a layer set in the layers editor (LayersDialog.vue),
// rendered on the server: the view wraps it, and an edit is one line.

import { describe, expect, test } from 'vitest';

import LayersDialog from '@/components/builder/dialogs/LayersDialog.vue';

import { attribute, sampleDocument, tagOf } from './fixtures.js';
import { renderWithStore } from './render.js';

const LONG =
  'The levels of the Purdue model, from the enterprise network at level 5 ' +
  'down to the physical process at level 0, with the industrial DMZ between ' +
  'the business and the control networks.';

const SET = {
  id: 'builtin:purdue',
  name: 'Purdue Model',
  description: LONG,
  iconKey: '',
  icon: '',
  matchConflict: 'lowest',
  layerOutlines: false,
  builtin: true,
  version: 1,
  layers: [{ id: 0, label: 'L0 Process' }],
};

async function render(set) {
  const { html } = await renderWithStore(LayersDialog, {
    props: { mode: 'library', setId: set.id },
    prepare(store) {
      store.doc = sampleDocument().doc;
      store.layerSets = {
        ...store.layerSets,
        status: 'ready',
        loaded: true,
        items: [set],
        defaultId: null,
      };
    },
  });

  return html;
}

describe('the description of a layer set', () => {
  test('a set that cannot change shows all of its description in a read-only text area that wraps', async () => {
    const html = await render(SET);
    const field = tagOf(html, 'layers-set-description');
    const start = html.indexOf(field) + field.length;

    expect(field.startsWith('<textarea')).toBe(true);
    expect(field).toContain('readonly');
    expect(attribute(field, 'id')).toBe('layers-set-description');
    expect(attribute(field, 'rows')).toBe('2');
    expect(html.slice(start, html.indexOf('</textarea>', start))).toBe(LONG);
    expect(html).toContain('<label for="layers-set-description"');
  });

  test('a set that can change keeps a one-line field, as no line break is allowed', async () => {
    const html = await render({ ...SET, id: 'plant', builtin: false });
    const field = tagOf(html, 'layers-set-description');

    expect(field.startsWith('<input')).toBe(true);
    expect(attribute(field, 'type')).toBe('text');
    expect(attribute(field, 'value')).toBe(LONG);
    expect(field).not.toContain('readonly');
  });
});
