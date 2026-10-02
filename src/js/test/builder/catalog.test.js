import { readFileSync } from 'node:fs';

import { describe, expect, test } from 'vitest';

import { iconKeyForSpec, nodeIconKey } from '@/builder/catalog.js';

// The server derives a generated device's icon from the same table
// (TestIconKeyForSpecMatchesFrontEnd in types/builder), so a node gets the
// same icon whether it was made here or generated there.
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

  test('a switch and a note have the icon of their kind', () => {
    expect(nodeIconKey({ kind: 'switch', switch: { iconKey: 'router' } })).toBe(
      'switch',
    );
    expect(nodeIconKey({ kind: 'note' })).toBe('vlan');
  });
});
