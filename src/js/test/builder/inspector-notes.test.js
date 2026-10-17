import { describe, expect, test } from 'vitest';

import bundle from '@/builder/schema/builder-v1.schema.json';
import {
  isMultilineList,
  labelWholeValue,
} from '@/components/builder/inspector/control.js';

describe('node notes in the Inspector', () => {
  test('a device spec takes general.notes, a list of text', () => {
    for (const kind of ['minimega_node', 'external_node']) {
      const general = bundle.$defs[`phenix.v1.${kind}`].properties.general;

      expect(general.properties.notes).toMatchObject({
        type: 'array',
        maxItems: 100,
        items: { type: 'string', minLength: 1, maxLength: 4096 },
      });
    }
  });

  test('each note is a text area', () => {
    expect(isMultilineList('spec.general.notes')).toBe(true);
    expect(isMultilineList('spec.commands')).toBe(false);
    expect(isMultilineList('spec.hardware.drives')).toBe(false);
    expect(isMultilineList(undefined)).toBe(false);
  });

  test('labelWholeValue adds options to the control it labels', () => {
    const control = { type: 'Control', scope: '#', options: { focus: true } };
    const layout = { type: 'VerticalLayout', elements: [control] };

    expect(labelWholeValue(control, 'Note 1', { multi: true })).toEqual({
      type: 'Control',
      scope: '#',
      label: 'Note 1',
      options: { focus: true, multi: true },
    });
    expect(labelWholeValue(layout, 'Note 2', { multi: true })).toEqual({
      type: 'VerticalLayout',
      elements: [
        {
          type: 'Control',
          scope: '#',
          label: 'Note 2',
          options: { focus: true, multi: true },
        },
      ],
    });
    expect(labelWholeValue(control, 'Command 1')).toEqual({
      ...control,
      label: 'Command 1',
    });
  });
});
