// @vitest-environment node
// The app's styles, read from compiled main.scss, the components' <style>
// blocks and builder.css: happy-dom lays nothing out, so these tests read the rules.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

import {
  colorChannels,
  contrastRatio,
  iconInk,
  needsCasing,
} from '@/builder/colors.js';
import { LIGHT_COLORS } from '@/builder/drawio.js';
import { DEFAULT_NETWORK_COLORS } from '@/builder/model.js';

import {
  componentDeclarations,
  contrast,
  declarations,
} from './helpers/css.js';

// A dialog's title wraps inside its head's padding; Bulma's own rule stops it
// shrinking, so a long user or VM name would reach the dialog's edge.
describe('dialog titles', () => {
  it('lets a long dialog title shrink and wrap', () => {
    expect(declarations('.modal-card-title')).toMatchObject({
      'flex-shrink': '1',
      'min-width': '0',
      'overflow-wrap': 'anywhere',
    });
  });
});

// A default button keeps white text when focused or pressed: Bulma's #363636
// is 2.2:1 on its gray, as when a closed dialog returns focus to its button.
describe('buttons', () => {
  it.each(['.button:focus', '.button:active'])(
    'gives %s white text',
    (selector) => {
      expect(declarations(selector)).toMatchObject({ color: 'white' });
    },
  );
});

// The header on a desktop-wide screen: with every page link and download it
// is wider than most screens, so it wraps onto a second row rather than push
// its right end (refresh status, jobs, Logout) off-screen. Up to 1400 px its
// links show only their icons, and in the burger menu their text again.
describe('AppHeader', () => {
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
});

// The page load bar: a moving segment in the theme's cyan, seen on the dark
// header, and a still bar for a user who asks for reduced motion.
describe('PageLoadBar', () => {
  const HEADER = '#4a4a4a';

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

describe('ExperimentSummary', () => {
  // DOM order: scenarios, topology, hosts, VMs, VLANs, start
  it('fills every row of tiles, with or without a start or hosts, in a narrow or wide card', () => {
    const file = 'src/components/experiment/ExperimentSummary.vue';
    const span = (selectors, wide) => {
      let found;
      for (const media of wide ? [null, '(min-width: 44rem)'] : [null]) {
        for (const selector of selectors.filter(Boolean)) {
          const value = componentDeclarations(file, selector, media)[
            'grid-column'
          ];
          if (value) found = Number(value.replace('span ', ''));
        }
      }
      return found;
    };
    for (const start of [false, true]) {
      for (const hosts of [false, true]) {
        for (const wide of [false, true]) {
          const tiles = [
            [
              '.exp-fact-scenarios',
              start && hosts && '.has-start.has-hosts .exp-fact-scenarios',
            ],
            ['.exp-fact-topology'],
            hosts && ['.exp-fact-hosts', start && '.has-start .exp-fact-hosts'],
            [
              '.exp-fact-vms',
              !start &&
                !hosts &&
                '.exp-summary-facts:not(.has-hosts):not(.has-start) .exp-fact-vms',
              start && '.has-start .exp-fact-vms',
            ],
            ['.exp-fact-vlans'],
            start && ['.exp-fact-started'],
          ].filter(Boolean);
          const rows = [0];
          for (const tile of tiles) {
            const n = span(tile, wide);
            if (rows.at(-1) + n > 12) rows.push(0);
            rows[rows.length - 1] += n;
          }
          expect({ start, hosts, wide, rows }).toEqual({
            start,
            hosts,
            wide,
            rows: [12, 12],
          });
        }
      }
    }
    expect(componentDeclarations(file, '.exp-fact-vlans')).toEqual({
      'grid-column': 'span 2',
    });
  });

  // "2026-10-28T14:03:22-06:00 (59 minutes ago)" fits on one line in 7 of the
  // 12 columns of a 44rem card, and "18 running of 25" in 3.
  it('widens the start over the counts in a wide card', () => {
    const file = 'src/components/experiment/ExperimentSummary.vue';
    expect(
      componentDeclarations(file, '.content dl.exp-summary-facts'),
    ).toMatchObject({ 'container-type': 'inline-size' });
    expect(componentDeclarations(file, '.exp-fact-started')).toEqual({
      'grid-column': 'span 6',
    });
    expect(
      componentDeclarations(file, '.exp-fact-started', '(min-width: 44rem)'),
    ).toEqual({ 'grid-column': 'span 7' });
    expect(
      componentDeclarations(
        file,
        '.has-start .exp-fact-vms',
        '(min-width: 44rem)',
      ),
    ).toEqual({ 'grid-column': 'span 3' });
  });
});

describe('VmPickFilter', () => {
  // a full-width glyph such as the server's fills its icon box, so Bulma's
  // 0.25em after the box left it touching the title
  it('leave room between the icon and the title', () => {
    expect(
      componentDeclarations(
        'src/components/experiment/VmPickFilter.vue',
        '.vm-pick-filter-trigger :deep(.icon:first-child:not(:last-child))',
      ),
    ).toEqual({ 'margin-inline-end': '0.5em' });
  });
});

describe('Experiments page', () => {
  // The table fits a 1,024 px screen: the cells pad less below 1,280 px, and
  // a start time wraps between its date, time and age (see timeWithAge.test).
  it('keeps the table within a desktop screen', () => {
    const style = (selector, media) =>
      componentDeclarations('src/views/Experiments.vue', selector, media);
    const narrow = 'min-width: 769px) and (max-width: 1279px';
    for (const cell of ['th', 'td']) {
      const selector = `.exp-table :deep(${cell}:not(.exp-select-cell))`;
      expect(style(selector, narrow)).toEqual({ 'padding-inline': '0.375em' });
      expect(style(selector)).toEqual({});
    }
  });

  it('draws every status, button and progress at one width', () => {
    const style = (selector) =>
      componentDeclarations('src/views/Experiments.vue', selector);
    expect(style('.status-width')).toEqual({ 'min-width': '7.5rem' });
    expect(style('.status-progress')).toEqual({
      width: '7.5rem',
      'margin-inline': 'auto',
    });
  });
});

describe('DiskDetailsCard colors', () => {
  const FILE = 'src/components/DiskDetailsCard.vue';
  // the card's surfaces: the head, the body, a section, a box in a section
  const HEAD = '#4f4f4f';
  const SECTION = '#484848';
  const BOX = '#3d3d3d';
  const fg = (selector) => componentDeclarations(FILE, selector).color;
  const bg = (selector) =>
    componentDeclarations(FILE, selector)['background-color'];

  it.each([
    ['meta text on the head', fg('.disk-card-meta'), HEAD],
    ['kind and type tags', fg('.disk-kind-tag'), bg('.disk-type-tag')],
    ['inject tag', fg('.disk-inject-tag'), bg('.disk-inject-tag')],
    ['section titles', fg('.disk-section-title'), SECTION],
    ['stat labels', fg('.disk-stat-label'), SECTION],
    ['empty notes', fg('.disk-empty'), SECTION],
    ['unavailable reasons', fg('.disk-unavailable'), SECTION],
    ['links on a section', fg('.disk-use-link'), SECTION],
    ['links on an issue box', fg('.disk-use-link'), bg('.format-issue')],
    ['links on a chain box', fg('.disk-use-link'), bg('.disk-chain-item')],
    ['chain box text', fg('.disk-chain-meta'), bg('.disk-chain-item')],
    [
      'chain box text on a missing image',
      fg('.disk-chain-meta'),
      bg('.disk-chain-item.is-missing'),
    ],
    ['command names on an issue box', fg('.disk-command-name'), BOX],
    [
      'partition IDs on an inject row',
      fg('.disk-part-name'),
      bg('.disk-partitions tr.is-inject td'),
    ],
    ['partition headings', fg('.disk-partitions th'), SECTION],
    ['problem text on a danger box', '#f5f5f5', bg('.disk-problems.is-danger')],
    ['problem text on a warning box', '#f5f5f5', bg('.disk-problems')],
  ])('keep %s at 7:1 or more', (_, text, surface) => {
    expect(contrast(text, surface)).toBeGreaterThanOrEqual(7);
  });

  // icons are not text: they need 3:1 (WCAG 1.4.11)
  it.each([
    [
      'danger icons on a danger box',
      fg('.disk-problems li.is-danger .icon'),
      bg('.disk-problems.is-danger'),
    ],
    [
      'warning icons on a warning box',
      fg('.disk-problems li.is-warning .icon'),
      bg('.disk-problems'),
    ],
  ])('keep %s at 3:1 or more', (_, icon, surface) => {
    expect(contrast(icon, surface)).toBeGreaterThanOrEqual(3);
  });

  // a disabled control needs no contrast, but its text stays readable and its
  // dashed outline shows on the section, which it has no fill to hide
  const disabled =
    ".disk-action-buttons :deep(.button[aria-disabled='true']:not(.is-inverted))";

  it('keeps disabled action text at 4.5:1 or more', () => {
    expect(bg(disabled)).toBe('transparent');
    expect(contrast(fg(disabled), SECTION)).toBeGreaterThanOrEqual(4.5);
  });

  it('draws a disabled action as a dashed outline at 3:1 or more', () => {
    const rule = componentDeclarations(FILE, disabled);
    expect(rule['border-style']).toBe('dashed');
    expect(contrast(rule['border-color'], SECTION)).toBeGreaterThanOrEqual(3);
  });
});

describe('VmDetailsCard', () => {
  const FILE = 'src/components/experiment/VmDetailsCard.vue';
  const SECTION = '#484848';
  const FAMILIES = ['linux', 'windows', 'router', 'other'];

  // six-digit form of a #rgb or #rrggbb color
  const fullHex = (hex) =>
    hex.length === 4 ? `#${[...hex.slice(1)].map((c) => c + c).join('')}` : hex;

  describe('the OS tag colors', () => {
    const style = readFileSync(join(import.meta.dirname, '..', FILE), 'utf8');

    it.each(FAMILIES)('give the %s family text of 4.5:1 or more', (family) => {
      const rule = new RegExp(
        `\\.vm-os-tag\\.is-os-${family}\\s*\\{([^}]*)\\}`,
      ).exec(style)?.[1];
      expect(rule).toBeDefined();
      const background = /background-color:\s*(#[0-9a-f]+)/i.exec(rule)[1];
      const text = /(?:^|\s)color:\s*(#[0-9a-f]+)/i.exec(rule)[1];
      expect(
        contrast(fullHex(background), fullHex(text)),
      ).toBeGreaterThanOrEqual(4.5);
    });
  });

  it.each([
    ".vm-power-buttons :deep(.button[aria-disabled='true']:not(.is-inverted))",
    ".vm-tool-buttons :deep(.button[aria-disabled='true']:not(.is-inverted))",
    ".vm-section-actions .button[aria-disabled='true']:not(.is-inverted)",
  ])('draw %s as a dashed outline with readable text', (selector) => {
    const rule = componentDeclarations(FILE, selector);
    expect(rule['background-color']).toBe('transparent');
    expect(rule['border-style']).toBe('dashed');
    expect(contrast(rule['border-color'], SECTION)).toBeGreaterThanOrEqual(3);
    expect(contrast(rule.color, SECTION)).toBeGreaterThanOrEqual(4.5);
  });

  it('is the larger modal: console left, details right', () => {
    expect(
      componentDeclarations(
        'src/components/experiment/VmDetailsCard.vue',
        '.vm-card--console',
      ),
    ).toMatchObject({
      width: 'min(96rem, calc(100vw - 40px))',
      height: '90vh',
    });
    expect(
      componentDeclarations(
        'src/components/experiment/VmDetailsCard.vue',
        '.vm-console-column',
      ),
    ).toMatchObject({ width: '60%' });
    expect(
      componentDeclarations('src/vnc/VncPreview.vue', '.vnc-preview-screen'),
    ).toMatchObject({ 'aspect-ratio': '16 / 10' });
  });
});

describe('VncTile', () => {
  it('keeps the bar shown while focus is in the tile', () => {
    expect(
      componentDeclarations(
        'src/vnc/VncTile.vue',
        '.vnc-tile:focus-within .vnc-tile-bar',
      ),
    ).toMatchObject({ opacity: '1', 'pointer-events': 'auto' });
  });
});

describe('VncTiles', () => {
  it('writes the VM names at 7:1 or more on their bar', () => {
    const style = componentDeclarations(
      'src/components/experiment/VncTiles.vue',
      '.vnc-tile-name',
    );
    expect(style['background-color']).toBe('#4d4d4d');
    expect(
      contrast('#f5f5f5', style['background-color']),
    ).toBeGreaterThanOrEqual(7);
  });
});

describe('Tunneler page', () => {
  it('sets the quick start text on a panel, at 7:1 or more', () => {
    const panel = componentDeclarations(
      'src/views/Tunneler.vue',
      '#quick-start',
    );
    const text = componentDeclarations('src/views/Tunneler.vue', 'p');
    const code = componentDeclarations('src/views/Tunneler.vue', 'code');

    expect(panel['background-color']).toBe('#484848');
    expect(
      contrast(
        text.color.replace(' !important', ''),
        panel['background-color'],
      ),
    ).toBeGreaterThanOrEqual(7);
    expect(
      contrast(code.color, code['background-color']),
    ).toBeGreaterThanOrEqual(7);
  });
});

describe('LogDetailsModal', () => {
  it('wraps long values inside the dialog, scrolls itself, and keeps text at 7:1', () => {
    const style = (selector) =>
      componentDeclarations('src/components/LogDetailsModal.vue', selector);
    expect(style('.log-details')).toMatchObject({
      'max-height': 'calc(100vh - 40px)',
    });
    expect(style('.log-details .modal-card-body')).toMatchObject({
      'line-height': '1.5',
      'overflow-y': 'auto',
    });
    for (const selector of ['.log-field dt', '.log-field dd']) {
      expect(style(selector)['overflow-wrap']).toBe('anywhere');
    }
    expect(style('.log-details-raw')).toMatchObject({
      'white-space': 'pre-wrap',
      'overflow-wrap': 'anywhere',
      overflow: 'auto',
    });

    // the card is $modal-card-body-background-color
    const card = '#4a4a4a';
    const stripe = style('.log-field:nth-child(odd)')['background-color'];
    for (const back of [card, stripe]) {
      for (const text of ['.log-field dt', '.log-field dd']) {
        expect(contrast(style(text).color, back)).toBeGreaterThanOrEqual(7);
      }
    }
    const raw = style('.log-details-raw');
    expect(contrast(raw.color, raw['background-color'])).toBeGreaterThan(7);
    // the full message's border against the card and its own fill
    const border = raw.border.split(' ').at(-1);
    expect(contrast(border, card)).toBeGreaterThanOrEqual(3);
    expect(contrast(border, raw['background-color'])).toBeGreaterThanOrEqual(3);
  });
});

describe('ScorchRuns component output window', () => {
  const FILE = 'src/views/ScorchRuns.vue';
  const STAGES = [
    ['configure', 'stage-configure'],
    ['start', 'stage-start'],
    ['stop', 'stage-stop'],
    ['cleanup', 'stage-cleanup'],
    ['done', 'stage-done'],
  ];

  it.each(STAGES)('gives the %s tag text 7:1 or more on its fill', (_, cls) => {
    const rule = componentDeclarations(FILE, `.tag.${cls}`);
    expect(
      contrast(rule.color, rule['background-color']),
    ).toBeGreaterThanOrEqual(7);
  });

  it('gives each stage a fill of its own', () => {
    const fills = STAGES.map(
      ([, cls]) =>
        componentDeclarations(FILE, `.tag.${cls}`)['background-color'],
    );
    expect(new Set(fills).size).toBe(fills.length);
  });

  // the theme draws a plain tag white on #575757
  it('gives the plain tag of an unknown stage text 7:1 or more on its fill', () => {
    expect(declarations('.tag:not(body)')).toMatchObject({
      'background-color': '#575757',
      color: 'white',
    });
    expect(contrast('#ffffff', '#575757')).toBeGreaterThanOrEqual(7);
  });
});

// The dialog opens inside the running experiment page's .content, whose h2
// and table cell rules would restyle its title and cells.
describe('NetflowCaptures dialog', () => {
  const style = (selector) =>
    componentDeclarations(
      'src/components/experiment/NetflowCaptures.vue',
      selector,
    );

  it('gives its h2 title the size and spacing of other dialog titles', () => {
    expect(style('.netflow-captures-card .modal-card-title')).toEqual({
      'margin-bottom': '0',
      'font-size': '1.5rem',
      'font-weight': '400',
      'line-height': '1.25',
    });
  });

  // the theme's is-info fill gives white text 7.3:1
  it('gives the Recording tag the theme blue, which white text reads on', () => {
    expect(style('.netflow-captures-table .tag.is-info')).toEqual({});
    expect(declarations('.tag:not(body).is-info')).toMatchObject({
      'background-color': 'hsl(207, 61%, 33%)',
      color: 'hsl(0, 0%, 100%)',
    });
  });

  it('centers each row vertically on its download buttons', () => {
    expect(style('.netflow-captures-table td')).toEqual({
      'vertical-align': 'middle',
    });
  });
});

// The Builder's style sheet, builder.css: its theme tokens and the rules
// that draw the nodes and connections on the canvas.
describe('Builder canvas', () => {
  const css = readFileSync(
    join(import.meta.dirname, '..', 'src/builder/builder.css'),
    'utf8',
  );

  // A selected node gets is-selected: a border and inner ring in the
  // selection color, which keeps 3:1 against its background in each theme.
  describe('a selected node', () => {
    const CSS = css.replace(/\/\*[\s\S]*?\*\//g, '');

    // The custom properties a rule block sets, by name.
    function tokensOf(selector) {
      const start = CSS.indexOf(`${selector} {`);
      const body = CSS.slice(start, CSS.indexOf('}', start));

      return Object.fromEntries(
        [...body.matchAll(/(--bx-[\w-]+):\s*([^;]+);/g)].map(
          ([, name, value]) => [name, value.trim()],
        ),
      );
    }

    // The body of the rule with exactly this selector list, spaces collapsed.
    function ruleBody(selector) {
      const rule = [...CSS.matchAll(/([^{}]+)\{([^{}]*)\}/g)].find(
        ([, list]) => list.replace(/\s+/g, ' ').trim() === selector,
      );

      return rule?.[2].replace(/\s+/g, ' ').trim();
    }

    const LIGHT = tokensOf('.builder-root');
    const DARK = {
      ...LIGHT,
      ...tokensOf(".builder-root[data-builder-theme='dark']"),
    };

    it('the style sheet draws no badge on a selected node', () => {
      expect(CSS).not.toMatch(/is-selected[^{}]*::after/);
      expect(CSS).not.toContain('has-resize-frame');
    });

    it.each([
      ['light', LIGHT],
      ['dark', DARK],
    ])(
      'in the %s theme, the selection color is the accent mixed with 10%% white and keeps 3:1',
      (theme, tokens) => {
        const accent = colorChannels(tokens['--bx-accent']);
        const ring = colorChannels(tokens['--bx-selected-ring']);

        accent.slice(0, 3).forEach((channel, index) => {
          expect(
            Math.abs(ring[index] - (channel * 0.9 + 255 * 0.1)),
            theme,
          ).toBeLessThanOrEqual(1);
        });

        for (const behind of [
          '--bx-bg',
          '--bx-grid',
          '--bx-surface',
          '--bx-bg-alt',
          '--bx-selected-bg',
        ]) {
          expect(
            contrastRatio(tokens['--bx-selected-ring'], tokens[behind]),
            `${theme} ${behind}`,
          ).toBeGreaterThanOrEqual(3);
        }
      },
    );

    it('the ring and the stroke of a selection are 10% wider than 2px and 4px', () => {
      expect(LIGHT['--bx-selected-ring-width']).toBe('2.2px');
      expect(LIGHT['--bx-selected-stroke']).toBe('4.4px');
    });

    it('every kind of node draws its selection in the selection color', () => {
      const ring =
        'inset 0 0 0 var(--bx-selected-ring-width) var(--bx-selected-ring)';

      expect(ruleBody('.builder-node.is-selected')).toContain(
        'border-color: var(--bx-selected-ring);',
      );
      expect(ruleBody('.builder-node.is-selected')).toContain(ring);
      expect(ruleBody('.builder-node.is-selected')).toContain(
        'background: var(--bx-selected-bg);',
      );
      // A shape and an icon remove the shadow of a node, so they draw the
      // ring again, without the shadow. They keep the selection fill.
      const drawing = ruleBody(
        '.builder-node--shape.is-selected, .builder-node--icon.is-selected',
      );

      expect(drawing).toContain('border-color: var(--bx-selected-ring);');
      expect(drawing).toContain(ring);
      expect(drawing).not.toContain('--bx-shadow');
      expect(drawing).not.toContain('background');
      expect(ruleBody('.builder-canvas .builder-edge.is-selected')).toBe(
        'stroke-width: var(--bx-selected-stroke);',
      );
    });

    it('in forced colors, a selected node has a Highlight border', () => {
      const forced = CSS.slice(CSS.indexOf('@media (forced-colors: active)'));

      expect(forced.replace(/\s+/g, ' ')).toContain(
        '.builder-node.is-selected { border-color: Highlight; }',
      );
    });
  });

  // The icon is in the outline color, apart from the text, and keeps 3:1
  // against what it is drawn over in each theme, or takes the text color.
  describe('a node’s icon color', () => {
    // Each theme's value of a token: the light theme's first.
    const token = (name) =>
      [...css.matchAll(new RegExp(`${name}: (#[0-9a-f]{6});`, 'gi'))].map(
        (match) => match[1],
      );
    const [surface, selected, canvas, grid, text] = [
      '--bx-surface',
      '--bx-selected-bg',
      '--bx-bg-alt',
      '--bx-grid',
      '--bx-text',
    ].map(token);

    it('is the theme’s accent color without an outline, 3:1 on every node ground', () => {
      const icon = token('--bx-accent');

      expect(icon).toHaveLength(2);
      [0, 1].forEach((theme) => {
        for (const ground of [surface, selected, canvas, grid]) {
          expect(contrastRatio(icon[theme], ground[theme])).toBeGreaterThan(3);
        }
        // Apart from the text: not the text's color.
        expect(icon[theme]).not.toBe(text[theme]);
      });
      expect(iconInk('')).toEqual({ light: false, dark: false });
      expect(iconInk('var(--bx-net-3)')).toEqual({ light: false, dark: false });
    });
  });

  describe('network colors', () => {
    // The hue, in degrees, and the saturation of a #rrggbb color.
    function hsl(hex) {
      const [r, g, b] = hex
        .slice(1)
        .match(/../g)
        .map((pair) => parseInt(pair, 16) / 255);
      const [max, min] = [Math.max(r, g, b), Math.min(r, g, b)];
      const delta = max - min;
      const sector =
        delta === 0
          ? 0
          : max === r
            ? (g - b) / delta
            : max === g
              ? (b - r) / delta + 2
              : (r - g) / delta + 4;

      return {
        hue: (sector * 60 + 360) % 360,
        saturation: delta === 0 ? 0 : delta / (1 - Math.abs(max + min - 1)),
      };
    }

    // Plum was drawn in slate: --bx-net-7 was a grey.
    it("each theme's network token is its suggested color's hue, and keeps 3:1", () => {
      // The light theme's, then the dark theme's.
      const tokens = [...css.matchAll(/--bx-net-(\d): (#[0-9a-f]{6});/gi)];

      expect(tokens).toHaveLength(2 * DEFAULT_NETWORK_COLORS.length);
      tokens.forEach(([, index, value], at) => {
        const theme = at < DEFAULT_NETWORK_COLORS.length ? 'light' : 'dark';
        const token = hsl(value);
        const suggested = hsl(DEFAULT_NETWORK_COLORS[index]);
        const apart = Math.abs(token.hue - suggested.hue);

        expect(Math.min(apart, 360 - apart), value).toBeLessThan(25);
        expect(token.saturation, value).toBeGreaterThan(0.3);
        expect(needsCasing(value)[theme], value).toBe(false);
      });
    });
  });

  // The rules that draw them: a selected node keeps its accent border, a
  // filled one its fill, and every line of text on a fill takes the ink.
  describe('node fills and outlines', () => {
    it('the stylesheet draws a fill and an outline from the custom properties', () => {
      const flat = css.replace(/\s+/g, ' ');

      expect(flat).toContain(
        '.builder-node--outlined { border-color: var(--bx-node-outline); }',
      );
      expect(flat).toContain(
        '.builder-node--filled, .builder-node--filled.is-selected { background: var(--bx-node-fill); color: var(--bx-node-ink); }',
      );
      expect(flat).toMatch(
        /\.builder-node--filled :is\( \.builder-node__kind, \.builder-node__meta, \.builder-node__comment, \.builder-node__origin \) \{ color: inherit; \}/,
      );
      // After the rule for a selected node, which it has to outrank for the
      // fill and must not for the border.
      expect(flat.indexOf('.builder-node--filled,')).toBeGreaterThan(
        flat.indexOf('.builder-node.is-selected {'),
      );
      expect(flat).toContain(
        ".builder-node--group[data-border='double'] { border-style: double; border-width: 4px; }",
      );
    });
  });

  // The draw.io download draws in the light theme's colors.
  describe('the draw.io download', () => {
    it('keeps the light colors of builder.css', () => {
      const block = css.slice(css.indexOf('.builder-root {'));
      const lightBlock = block.slice(0, block.indexOf('}'));

      for (const [token, color] of Object.entries(LIGHT_COLORS)) {
        expect(lightBlock, token).toMatch(new RegExp(`${token}: ${color};`));
      }
    });
  });
});
