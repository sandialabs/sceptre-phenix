// SVG and PNG export helpers of the State of Health graph.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  LEGEND,
  composeSvg,
  exportFileName,
  localUrls,
  pngScale,
} from '@/utils/soh/exportImage.js';

// What XML 1.0 never allows: C0 controls but tab, LF and CR, U+FFFE,
// U+FFFF and lone surrogates.
const INVALID_XML =
  // eslint-disable-next-line no-control-regex
  /[\u0000-\u0008\u000B\u000C\u000E-\u001F\uFFFE\uFFFF]|[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

describe('file names', () => {
  const when = new Date(2026, 8, 28, 7, 5, 9);

  it.each([
    [
      'names files <exp>-soh-<layout>-<local timestamp>.<ext>',
      ['enterprise-ot', 'layered', 'png'],
      'enterprise-ot-soh-layered-20260928-070509.png',
    ],
    [
      'keeps path separators out of names',
      ['../etc/passwd', 'force', 'svg'],
      'etc_passwd-soh-force-20260928-070509.svg',
    ],
    [
      'replaces characters some file systems refuse',
      ['my exp (1)', 'elk', 'gexf'],
      'my_exp_1-soh-elk-20260928-070509.gexf',
    ],
    [
      'names an experiment without a name',
      ['', 'radial', 'png'],
      'experiment-soh-radial-20260928-070509.png',
    ],
  ])('%s', (_, [experiment, layout, ext], want) => {
    expect(exportFileName(experiment, layout, ext, when)).toBe(want);
  });

  it('makes a safe name, stable under renaming, that reads back as the local time', () => {
    const date = fc
      .integer({ min: Date.UTC(2000, 0, 1), max: Date.UTC(2100, 0, 1) })
      .map((ms) => new Date(ms));
    const text = fc.string({ unit: 'binary', maxLength: 12 });
    fc.assert(
      fc.property(
        text,
        fc.constantFrom('png', 'svg', 'gexf'),
        date,
        (exp, ext, d) => {
          const name = exportFileName(exp, 'force', ext, d);
          expect(name).toMatch(/^[A-Za-z0-9-][A-Za-z0-9._-]*$/);
          const at = name.lastIndexOf('-soh-force-');
          // the name it gives is a name it keeps
          expect(exportFileName(name.slice(0, at), 'force', ext, d)).toBe(name);
          const [, y, mo, day, h, mi, sec] = name
            .slice(at + '-soh-force-'.length)
            .match(/^(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)(\d\d)\.(\w+)$/)
            .map(Number);
          const back = new Date(y, mo - 1, day, h, mi, sec);
          expect(back.toLocaleString()).toBe(d.toLocaleString());
        },
      ),
    );
  });
});

describe('SVG serialization', () => {
  const base = {
    graph: '<circle cx="10" cy="20" r="5" style="fill:#4F8F00"/>',
    defs: '<pattern id="switch"><image href="data:image/png;base64,AA=="/></pattern>',
    bbox: { x: 0, y: 0, width: 100, height: 50 },
    transform: { k: 1, x: 0, y: 0 },
    scale: 1,
    title: 'State of Health: a<b & "c"',
    subtitle: '3 VMs · filter: all',
  };

  it('fits the viewBox to the graph at the current zoom, with room for title and legend', () => {
    const out = composeSvg({
      ...base,
      bbox: { x: 10, y: -5, width: 100, height: 50 },
      transform: { k: 2, x: 30, y: 40 },
      scale: 1.5,
    });
    // graph area 100x50 at zoom 2 and 1.5 px per unit = 300x150 px
    expect(out.width).toBeGreaterThanOrEqual(300);
    expect(out.height).toBe(150 + 56 + 40 + 20);
    expect(out.svg).toContain(`viewBox="0 0 ${out.width} ${out.height}"`);
    expect(out.svg).toContain(`width="${out.width}" height="${out.height}"`);
    // Zoom 2 x 1.5 px per unit; the fitted box puts its corner (10, -5) at the
    // graph area's left edge, under the 56 px title.
    const x = (out.width - 300) / 2 - 10 * 3;
    expect(out.svg).toContain(
      `<g transform="translate(${x},${56 + 5 * 3}) scale(3)">`,
    );
  });

  it('is a standalone SVG document with escaped text, defs and a legend', () => {
    const { svg } = composeSvg(base);
    expect(svg.startsWith('<svg xmlns="http://www.w3.org/2000/svg"')).toBe(
      true,
    );
    expect(svg).toContain('xmlns:xlink="http://www.w3.org/1999/xlink"');
    expect(svg).toContain('State of Health: a&lt;b &amp; &quot;c&quot;');
    expect(svg).not.toContain('a<b');
    expect(svg).toContain('<defs><pattern id="switch">');
    expect(svg).toContain(base.graph);
    for (const item of LEGEND) expect(svg).toContain(`>${item.label}<`);
    // the VLAN legend entry uses the graph's VLAN icon when defs carry it
    expect(svg).toContain('fill="url(#switch)"');
    expect(composeSvg({ ...base, defs: '' }).svg).not.toContain('url(#switch)');
    expect(svg.endsWith('</svg>')).toBe(true);
  });

  // text as XML 1.0 allows it, with the characters markup gives meaning to
  const xmlText = fc.string({
    unit: fc.oneof(
      fc.constantFrom('<', '>', '&', '"', "'", '\t', '\n', '\r', ' '),
      fc.string({ unit: 'grapheme', minLength: 1, maxLength: 1 }),
    ),
  });
  const parse = (svg) =>
    new DOMParser().parseFromString(svg, 'image/svg+xml').documentElement;

  it('reads back the title and subtitle as given', () => {
    fc.assert(
      fc.property(xmlText, xmlText, (title, subtitle) => {
        const doc = parse(composeSvg({ ...base, title, subtitle }).svg);
        const texts = [...doc.children].filter((e) => e.tagName === 'text');
        expect(doc.querySelector('title').textContent).toBe(title);
        expect(texts.map((t) => t.textContent)).toEqual([title, subtitle]);
      }),
    );
  });

  it('drops characters XML does not allow', () => {
    fc.assert(
      fc.property(fc.string({ unit: 'binary' }), (title) => {
        expect(composeSvg({ ...base, title }).svg).not.toMatch(INVALID_XML);
      }),
    );
  });

  it('draws the whole graph box centered between the title and the legend', () => {
    // mostly small coordinates, where the 3-decimal transform is exact enough
    const num = (min, max) =>
      fc.oneof(
        fc.integer({
          min: Math.ceil(Math.max(min, -50)),
          max: Math.floor(Math.min(max, 50)),
        }),
        fc.double({ min, max, noNaN: true }),
      );
    fc.assert(
      fc.property(
        fc.record({
          x: num(-1e4, 1e4),
          y: num(-1e4, 1e4),
          width: num(0, 1e4),
          height: num(0, 1e4),
        }),
        fc.record({ k: num(0.05, 20), x: num(-1e4, 1e4), y: num(-1e4, 1e4) }),
        num(0.25, 4),
        (bbox, transform, scale) => {
          const { svg, width, height } = composeSvg({
            ...base,
            bbox,
            transform,
            scale,
          });
          const [tx, ty, k] =
            /<g transform="translate\(([^,]+),([^)]+)\) scale\(([^)]+)\)">/
              .exec(svg)
              .slice(1)
              .map(Number);
          // where a graph coordinate lands, and how far off rounding may put it
          const at = (t, v) => t + k * v;
          const off = (v) => 0.001 * (1 + Math.abs(v));
          const [x0, x1] = [bbox.x, bbox.x + bbox.width];
          const [y0, y1] = [bbox.y, bbox.y + bbox.height];
          expect(width).toBeGreaterThanOrEqual(480);
          expect(at(tx, x0)).toBeGreaterThanOrEqual(20 - off(x0));
          // a box under 1 px is drawn 1 px wide
          expect(
            Math.abs(at(tx, x0) - (width - at(tx, x1))),
          ).toBeLessThanOrEqual(1 + off(x0) + off(x1));
          expect(Math.abs(at(ty, y0) - 56)).toBeLessThanOrEqual(off(y0));
          expect(at(ty, y1)).toBeLessThanOrEqual(height - 40 - 20 + off(y1));
        },
      ),
    );
  });

  it('points paint servers at the file, not the page', () => {
    expect(localUrls('url("http://host:3000/experiment/x/soh#switch")')).toBe(
      'url(#switch)',
    );
    expect(localUrls('url(#linux)')).toBe('url(#linux)');
    expect(localUrls('rgb(1, 2, 3)')).toBe('rgb(1, 2, 3)');
  });

  it('points any paint server at its fragment, and leaves the rest alone', () => {
    const id = fc.stringMatching(/^[A-Za-z][\w.-]{0,8}$/);
    // the page's address, as a computed style quotes it
    const page = fc
      .webUrl({ withQueryParameters: true })
      .filter((url) => !/["'()]/.test(url));
    const quote = fc.constantFrom('', '"', "'");
    const other = fc.string().filter((v) => !v.includes('url('));
    fc.assert(
      fc.property(
        other,
        page,
        quote,
        id,
        other,
        (before, url, q, frag, after) => {
          const value = `${before}url(${q}${url}#${frag}${q})${after}`;
          const local = localUrls(value);
          expect(local).toBe(`${before}url(#${frag})${after}`);
          expect(localUrls(local)).toBe(local);
        },
      ),
    );
  });
});

describe('PNG size', () => {
  // the page asks for 2x
  it('renders at the scale asked unless the canvas would be too big', () => {
    expect(pngScale(1000, 800, 2)).toBe(2);
    expect(pngScale(12000, 1000, 2)).toBeCloseTo(16384 / 12000);
    const s = pngScale(12000, 12000, 2);
    expect(12000 * s * 12000 * s).toBeLessThanOrEqual(16384 * 16384 * 0.25 + 1);
  });

  it('never asks for more than wanted or than a canvas holds', () => {
    const side = fc.double({ min: 1, max: 1e5, noNaN: true });
    fc.assert(
      fc.property(
        side,
        side,
        fc.double({ min: 0.1, max: 8, noNaN: true }),
        (w, h, wanted) => {
          const s = pngScale(w, h, wanted);
          expect(s).toBeLessThanOrEqual(wanted);
          expect(s).toBeGreaterThanOrEqual(0.1);
          // only the 0.1 floor may push a huge drawing past the limits
          if (s > 0.1) {
            expect(Math.max(w, h) * s).toBeLessThanOrEqual(16384 * (1 + 1e-12));
            expect(w * h * s * s).toBeLessThanOrEqual(
              16384 * 16384 * 0.25 * (1 + 1e-12),
            );
          }
          // a drawing that fits is drawn at the scale asked
          const fits =
            Math.max(w, h) * wanted <= 16384 &&
            w * h * wanted ** 2 <= 16384 ** 2 / 4;
          if (fits) expect(s).toBe(wanted);
        },
      ),
    );
  });
});
