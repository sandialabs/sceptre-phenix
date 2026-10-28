import { readFileSync } from 'node:fs';

import { describe, expect, test } from 'vitest';

import { iconKeyForSpec, isIconKey, nodeIconKey } from '@/builder/catalog.js';
import { ICON_ORDER, iconLabel } from '@/builder/iconLabels.js';
import builderSchemaV1 from '@/builder/schema/builder-v1.schema.json';

// The server derives a generated device's icon from the same table
// (TestIconKeyForSpecMatchesFrontEnd in types/builder).
describe('icon keys shared with the server', () => {
  const table = JSON.parse(
    readFileSync(
      new URL(
        '../../../go/types/builder/testdata/icon-keys.json',
        import.meta.url,
      ),
    ),
  );

  test.each(table.cases.map((entry) => [JSON.stringify(entry.spec), entry]))(
    '%s',
    (_, entry) => {
      expect(iconKeyForSpec(entry.spec)).toBe(entry.iconKey);
    },
  );
});

// The registry has the keys of the server's schema (iconKeys in
// types/builder icons.go), and each key has a label.
test('the icon registry matches the schema, and each key has a label', () => {
  const keys = builderSchemaV1.$defs.iconKey.enum.filter(Boolean);

  expect([...ICON_ORDER].sort()).toEqual(keys);
  expect(keys.every(isIconKey)).toBe(true);
  expect(isIconKey('')).toBe(false);
  expect(iconLabel('cell-tower')).toBe('Cell Tower');
  expect(iconLabel('toaster')).toBe('toaster');
});

describe("a node's icon", () => {
  test('a device has its own, and falls back to the server icon', () => {
    expect(nodeIconKey({ kind: 'device', device: { iconKey: 'router' } })).toBe(
      'router',
    );
    expect(nodeIconKey({ kind: 'device', device: { iconKey: 'nope' } })).toBe(
      'server',
    );
    expect(nodeIconKey(undefined)).toBe('server');
  });

  test('a group has the one it was given, else the group icon', () => {
    expect(nodeIconKey({ kind: 'group', group: { title: 'Core' } })).toBe(
      'container',
    );
    expect(nodeIconKey({ kind: 'group', group: { iconKey: '' } })).toBe(
      'container',
    );
    expect(nodeIconKey({ kind: 'group', group: { iconKey: 'firewall' } })).toBe(
      'firewall',
    );
    // A key the registry does not have is not drawn: no path, no URL.
    expect(
      nodeIconKey({ kind: 'group', group: { iconKey: 'https://x/y.png' } }),
    ).toBe('container');
  });

  test('a switch, a note, a shape and a line have the icon of their kind', () => {
    expect(nodeIconKey({ kind: 'switch', switch: { iconKey: 'router' } })).toBe(
      'switch',
    );
    expect(nodeIconKey({ kind: 'note' })).toBe('sticky-note');
    expect(nodeIconKey({ kind: 'shape' })).toBe('rectangle');
    expect(nodeIconKey({ kind: 'line' })).toBe('line');
  });
});
