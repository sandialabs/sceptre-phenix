// Where a layer's name takes the layer's color (layerLook.js): in a theme
// whose backgrounds all keep 4.5:1 against it.

import { describe, expect, test } from 'vitest';

import { contrastRatio } from '@/builder/colors.js';
import {
  LAYER_TEXT_BACKGROUNDS,
  MIN_TEXT_CONTRAST,
  hasLayerLook,
  layerInk,
  layerLook,
} from '@/builder/layerLook.js';

import { ICON_DATA } from './png.js';

describe('layerInk', () => {
  test('a dark color reads in the light theme only, a light one in the dark theme only', () => {
    expect(layerInk('#0f766e')).toEqual({ light: true, dark: false });
    expect(layerInk('#7dd3fc')).toEqual({ light: false, dark: true });
  });

  test('a mid color that reads in neither theme gets a swatch in both', () => {
    expect(layerInk('#8b5cf6')).toEqual({ light: false, dark: false });
  });

  test('no color, or a value that is not #rrggbb, never colors the name', () => {
    for (const value of [undefined, null, '', 'red', '#fff', '#12345g', 7]) {
      expect(layerInk(value)).toEqual({ light: false, dark: false });
    }
  });

  test('every background of a theme counts', () => {
    // Reads on white, not on the light theme's alternate background.
    const color = ['#767676', '#757575', '#737373'].find(
      (value) =>
        contrastRatio(value, '#ffffff') >= MIN_TEXT_CONTRAST &&
        contrastRatio(value, LAYER_TEXT_BACKGROUNDS.light[1]) <
          MIN_TEXT_CONTRAST,
    );

    expect(color).toBeTruthy();
    expect(layerInk(color).light).toBe(false);
  });

  test('no one color reads in both themes', () => {
    for (let gray = 0; gray < 256; gray += 1) {
      const hex = `#${gray.toString(16).padStart(2, '0').repeat(3)}`;
      const ink = layerInk(hex);

      expect(ink.light && ink.dark).toBe(false);
    }
  });
});

describe('layerLook', () => {
  test('a valid icon key, a custom icon that resolves and a #rrggbb color show', () => {
    const icons = { plc: { data: ICON_DATA } };
    const library = {
      lookup: (name) => (name === 'shared' ? icons.plc : null),
    };

    expect(
      layerLook({ iconKey: 'server', color: '#0f766e' }, icons, library),
    ).toEqual({ iconKey: 'server', src: '', color: '#0f766e' });
    expect(layerLook({ icon: 'plc' }, icons).src).toMatch(/^data:image\/png/);
    expect(layerLook({ icon: 'shared' }, null, library).src).toMatch(
      /^data:image\/png/,
    );
    expect(hasLayerLook({ color: '#0f766e' })).toBe(true);
    expect(hasLayerLook({ icon: 'plc' }, icons)).toBe(true);
  });

  test('values that do not show give nothing to show', () => {
    for (const layer of [
      undefined,
      {},
      { color: 'red' },
      { color: '#fff' },
      { iconKey: 'not-an-icon-key' },
      { icon: 'missing' },
    ]) {
      expect(layerLook(layer), JSON.stringify(layer)).toEqual({
        iconKey: '',
        src: '',
        color: '',
      });
      expect(hasLayerLook(layer, {}, { lookup: () => null })).toBe(false);
    }
  });
});
