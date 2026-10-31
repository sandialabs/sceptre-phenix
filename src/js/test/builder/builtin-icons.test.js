// The built-in icons that the Icons tab lists and counts, and a built-in icon
// as a standalone SVG file and as a PNG file.

import { describe, expect, test, vi } from 'vitest';

import {
  BUILTIN_ICON_COLOR,
  BUILTIN_ICON_FILE_SIZE,
  BUILTIN_ICON_OWNER,
  builtinIconMatches,
  builtinIconSvg,
  builtinIcons,
  iconsTabCount,
  saveBuiltinIconFile,
} from '@/builder/builtinIcons.js';
import { isIconKey } from '@/builder/catalog.js';
import { ICON_ORDER, iconLabel } from '@/builder/iconLabels.js';
import { MAX_ICON_PIXELS } from '@/builder/icons.js';

import { base64Of, bytesOf, png } from './png.js';

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

describe('iconsTabCount', () => {
  test('counts the built-in icons and the library’s icons once the library is read', () => {
    const builtin = builtinIcons().length;

    expect(iconsTabCount({ loaded: true, icons: [] })).toBe(builtin);
    expect(iconsTabCount({ loaded: true, icons: [{}, {}] })).toBe(builtin + 2);
    expect(iconsTabCount({ loaded: true })).toBe(builtin);
    // Until the library is read, the tab shows no count.
    expect(iconsTabCount({ loaded: false, icons: [{}] })).toBeNull();
    expect(iconsTabCount(null)).toBeNull();
  });
});

describe('saveBuiltinIconFile', () => {
  const PNG = base64Of(png(2, 2, [27, 35, 48, 255]));

  test('draws the SVG with rasterize and saves the PNG named after the key', async () => {
    const saveAs = vi.fn();
    const rasterize = vi.fn(async () => ({ name: '', data: PNG }));

    expect(
      await saveBuiltinIconFile({ key: 'router' }, ROUTER, {
        saveAs,
        rasterize,
      }),
    ).toBe('router.png');

    const [[svg]] = rasterize.mock.calls;

    expect(svg.type).toBe('image/svg+xml');
    expect(await svg.text()).toBe(
      builtinIconSvg({ markup: ROUTER, label: 'Router' }),
    );
    expect(await svg.text()).toContain(
      `width="${BUILTIN_ICON_FILE_SIZE}" height="${BUILTIN_ICON_FILE_SIZE}"`,
    );
    expect(BUILTIN_ICON_FILE_SIZE).toBe(MAX_ICON_PIXELS);

    const [[blob, fileName]] = saveAs.mock.calls;

    expect(fileName).toBe('router.png');
    expect(blob.type).toBe('image/png');
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(bytesOf(PNG));
  });

  test('saves nothing without a drawing, for another key, or for a drawing that is not a PNG', async () => {
    const saveAs = vi.fn();
    const rasterize = vi.fn(async () => ({ name: '', data: PNG }));
    const options = { saveAs, rasterize };

    expect(await saveBuiltinIconFile({ key: 'router' }, '', options)).toBe('');
    expect(await saveBuiltinIconFile({ key: 'plc.png' }, ROUTER, options)).toBe(
      '',
    );
    expect(rasterize).not.toHaveBeenCalled();
    expect(
      await saveBuiltinIconFile({ key: 'router' }, ROUTER, {
        saveAs,
        rasterize: async () => ({ data: 'not base64!' }),
      }),
    ).toBe('');
    await expect(
      saveBuiltinIconFile({ key: 'router' }, ROUTER, {
        saveAs,
        rasterize: async () => {
          throw new Error('no canvas');
        },
      }),
    ).rejects.toThrow('no canvas');
    expect(saveAs).not.toHaveBeenCalled();
  });
});
