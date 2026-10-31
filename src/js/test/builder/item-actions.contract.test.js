// The item actions (builder/itemActions.js): each action's label, icon and
// color, as the Builder's buttons show them.

import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import { iconPath } from '@/builder/iconPaths.js';
import { ITEM_ACTIONS, itemAction, toneClass } from '@/builder/itemActions.js';

describe('the item actions', () => {
  test('Edit and Open are blue, Delete is red, and the rest have the default color', () => {
    const table = Object.fromEntries(
      Object.entries(ITEM_ACTIONS).map(([name, spec]) => [
        name,
        [spec.label, spec.icon, toneClass(spec.tone)],
      ]),
    );

    expect(table).toEqual({
      edit: ['Edit', 'pencil', 'builder-button--primary'],
      delete: ['Delete', 'trash', 'builder-button--danger'],
      publish: ['Publish', 'publish', ''],
      duplicate: ['Duplicate', 'copy', ''],
      open: ['Open', 'folder-open', 'builder-button--primary'],
      view: ['View', 'eye', ''],
      download: ['Download', 'download', ''],
    });
  });

  // An unknown icon key draws the server icon, so each action has its own.
  test('each action has an icon of its own, apart from the fallback', () => {
    const drawings = Object.values(ITEM_ACTIONS).map((spec) =>
      iconPath(spec.icon),
    );

    expect(new Set(drawings).size).toBe(drawings.length);
    expect(drawings).not.toContain(iconPath('server'));
  });

  test('a name that is not an action has no action, and a tone that is not known has no color', () => {
    const names = Object.keys(ITEM_ACTIONS);

    fc.assert(
      fc.property(fc.string(), (name) => {
        expect(itemAction(name)).toBe(
          names.includes(name) ? ITEM_ACTIONS[name] : null,
        );
        expect([
          '',
          ...Object.values(ITEM_ACTIONS).map((spec) => toneClass(spec.tone)),
        ]).toContain(toneClass(name));
      }),
      { numRuns: 200 },
    );
    expect(itemAction('toString')).toBeNull();
    expect(toneClass('constructor')).toBe('');
  });
});
