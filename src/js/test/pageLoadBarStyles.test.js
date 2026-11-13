// @vitest-environment node
// The page load bar: a moving segment in the theme's cyan, seen on the dark
// header, and a still bar for a user who asks for reduced motion.
import { describe, expect, it } from 'vitest';

import { contrast, declarations } from './helpers/css.js';

const HEADER = '#4a4a4a';

describe('the page load bar styles', () => {
  it('moves a segment across a thin bar at the top of the window', () => {
    expect(declarations('.page-load-bar')).toMatchObject({
      position: 'fixed',
      height: '3px',
      'pointer-events': 'none',
    });
    const segment = declarations('.page-load-bar::before');
    expect(segment.animation).toMatch(/^page-load-slide /);
    expect(contrast(segment['background-color'], HEADER)).toBeGreaterThan(3);
  });

  it('shows a still bar, with no segment, for reduced motion', () => {
    const reduce = 'prefers-reduced-motion: reduce';
    const still = declarations('.page-load-bar', reduce);
    expect(contrast(still['background-color'], HEADER)).toBeGreaterThan(3);
    expect(declarations('.page-load-bar::before', reduce)).toEqual({
      content: 'none',
    });
  });
});
