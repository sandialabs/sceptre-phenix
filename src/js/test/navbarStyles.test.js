// @vitest-environment node
// The header on a desktop-wide screen: with every page link and download it
// is wider than most screens, so it wraps onto a second row rather than push
// its right end (refresh status, jobs, Logout) off-screen. Up to 1400 px its
// links show only their icons, and in the burger menu their text again.
import { expect, it } from 'vitest';

import { componentDeclarations, declarations } from './helpers/css.js';

// Bulma's navbar breakpoint, $desktop: where the burger menu gives way
const DESKTOP = 'min-width: 1024px';
const BURGER = 'max-width: 1023px';
const ICONS_ONLY = 'min-width: 1024px) and (max-width: 1399.98px';

it.each(['.navbar-menu', '.navbar-start', '.navbar-end'])(
  'lets %s wrap within the screen',
  (selector) => {
    expect(declarations(selector, DESKTOP)).toMatchObject({
      'flex-wrap': 'wrap',
      'flex-shrink': '1',
      'min-width': '0',
    });
  },
);

it('hides the link text, not its name, only between the burger menu and 1400 px', () => {
  expect(declarations('.navbar .nav-text', ICONS_ONLY)).toMatchObject({
    position: 'absolute',
    width: '1px',
    'clip-path': 'inset(50%)',
  });
  expect(declarations('.navbar .nav-text')).toEqual({});
  expect(declarations('.navbar .nav-text', BURGER)).toEqual({});
});

it('drops the dots and the link tooltips in the burger menu, where the text shows', () => {
  expect(
    componentDeclarations('src/components/AppHeader.vue', '.nav-dot', BURGER),
  ).toEqual({ display: 'none' });
  expect(
    declarations('.navbar .nav-tip > .tooltip-content', BURGER),
  ).toMatchObject({ display: 'none !important' });
});

it('opens the header menus in place in the burger menu', () => {
  expect(
    componentDeclarations(
      'src/components/NavMenu.vue',
      '.nav-menu-panel',
      BURGER,
    ),
  ).toMatchObject({ position: 'static' });
});

it.each([
  ['.button.is-primary', 'background-color', '#9a4420'],
  [
    '.navbar .button.nav-icon-button:focus-visible',
    'outline',
    '3px solid #ffd166',
  ],
])('gives %s the %s %s', (selector, property, value) => {
  expect(declarations(selector)[property]).toBe(value);
});
