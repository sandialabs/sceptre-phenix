// The built-in icons that the Icons tab lists, and a built-in icon as a
// standalone SVG file.

import { describe, expect, test, vi } from 'vitest';

import {
  BUILTIN_ICON_COLOR,
  BUILTIN_ICON_OWNER,
  builtinIconMatches,
  builtinIconSvg,
  builtinIcons,
  saveBuiltinIconFile,
} from '@/builder/builtinIcons.js';
import { isIconKey } from '@/builder/catalog.js';
import { ICON_ORDER, iconLabel } from '@/builder/iconLabels.js';

const ROUTER = '<g><circle cx="12" cy="12" r="9"/><path d="M8 12h8"/></g>';

describe('builtinIcons', () => {
  test('lists every key of ICON_ORDER the registry accepts, in that order, with its label', () => {
    const icons = builtinIcons();

    expect(icons.map((icon) => icon.key)).toEqual(ICON_ORDER.filter(isIconKey));
    expect(icons.map((icon) => icon.key)).toContain('printer');
    expect(icons.length).toBeGreaterThan(0);
    for (const icon of icons) {
      expect(icon).toEqual({
        key: icon.key,
        label: iconLabel(icon.key),
        builtin: true,
      });
    }
    expect(icons.find((icon) => icon.key === 'external')?.label).toBe(
      'External device',
    );
    expect(BUILTIN_ICON_OWNER).toBe('Built-in');
  });

  test('match a filter by label, key or owner, ignoring case', () => {
    const external = { key: 'external', label: 'External device' };

    expect(builtinIconMatches(external, '')).toBe(true);
    expect(builtinIconMatches(external, '  DEVICE ')).toBe(true);
    expect(builtinIconMatches(external, 'extern')).toBe(true);
    expect(builtinIconMatches(external, 'built-in')).toBe(true);
    expect(builtinIconMatches(external, 'router')).toBe(false);
  });
});

describe('builtinIconSvg', () => {
  test('is a standalone SVG of the drawing in a fixed color, with the label as its title', () => {
    const svg = builtinIconSvg({
      markup: '<path d="M1 1" fill="currentColor"/>',
      label: 'R&D <lab>',
    });

    expect(svg).toBe(
      '<?xml version="1.0" encoding="UTF-8"?>\n' +
        '<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" ' +
        `viewBox="0 0 24 24" fill="none" stroke="${BUILTIN_ICON_COLOR}" ` +
        'stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">' +
        '<title>R&amp;D &lt;lab&gt;</title>' +
        `<path d="M1 1" fill="${BUILTIN_ICON_COLOR}"/></svg>\n`,
    );
    expect(svg).not.toContain('currentColor');
    expect(builtinIconSvg({ markup: ROUTER, label: 'x', size: 24 })).toContain(
      'width="24" height="24"',
    );
  });
});

describe('saveBuiltinIconFile', () => {
  test('saves the SVG named after the key, and nothing without a drawing or for another key', async () => {
    const saveAs = vi.fn();

    expect(saveBuiltinIconFile({ key: 'router' }, ROUTER, { saveAs })).toBe(
      'router.svg',
    );

    const [[blob, fileName]] = saveAs.mock.calls;

    expect(fileName).toBe('router.svg');
    expect(blob.type).toBe('image/svg+xml');
    expect(await blob.text()).toBe(
      builtinIconSvg({ markup: ROUTER, label: 'Router' }),
    );

    expect(saveBuiltinIconFile({ key: 'router' }, '', { saveAs })).toBe('');
    expect(saveBuiltinIconFile({ key: 'plc.png' }, ROUTER, { saveAs })).toBe(
      '',
    );
    expect(saveAs).toHaveBeenCalledTimes(1);
  });
});
