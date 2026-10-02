// SVG and PNG export helpers of the State of Health graph.
import { describe, expect, it } from 'vitest';

import {
  LEGEND,
  composeSvg,
  exportFileName,
  localUrls,
  pngScale,
} from '@/utils/soh/exportImage.js';

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
    // Zoom 2 x 1.5 px per unit; the pan cancels out once the box is fitted,
    // leaving the box's corner (10, -5) at the graph area's left edge and
    // under the 56 px title.
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

  it('points paint servers at the file, not the page', () => {
    expect(localUrls('url("http://host:3000/experiment/x/soh#switch")')).toBe(
      'url(#switch)',
    );
    expect(localUrls('url(#linux)')).toBe('url(#linux)');
    expect(localUrls('rgb(1, 2, 3)')).toBe('rgb(1, 2, 3)');
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
});
