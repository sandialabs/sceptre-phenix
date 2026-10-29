// How the VM details card names and colors a VM's guest operating system.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { icon, library } from '@fortawesome/fontawesome-svg-core';
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { osTypeInfo } from '@/components/experiment/osType.js';
import { faLinux, faWindows } from '@/utils/icons.js';

const FAMILIES = ['linux', 'windows', 'router', 'other'];

// the WCAG contrast ratio of two #rrggbb colors
function contrast(a, b) {
  const luminance = (hex) => {
    const [r, g, bl] = [1, 3, 5].map((i) => {
      const c = parseInt(hex.slice(i, i + 2), 16) / 255;
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
  };
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// six-digit form of a #rgb or #rrggbb color
const fullHex = (hex) =>
  hex.length === 4 ? `#${[...hex.slice(1)].map((c) => c + c).join('')}` : hex;

describe('osTypeInfo', () => {
  it.each([
    ['linux', 'Linux', 'linux', 'linux'],
    ['centos', 'CentOS', 'linux', 'linux'],
    ['rhel', 'RHEL', 'linux', 'linux'],
    ['windows', 'Windows', 'windows', 'windows'],
    ['Windows', 'Windows', 'windows', 'windows'],
    ['vyos', 'VyOS', 'router', 'route'],
    ['vyatta', 'Vyatta', 'router', 'route'],
    ['minirouter', 'minirouter', 'router', 'route'],
    ['other', 'Other', 'other', 'desktop'],
    ['plan9', 'plan9', 'other', 'desktop'],
  ])('shows %s as %s, in the %s family', (osType, label, family, iconName) => {
    expect(osTypeInfo(osType)).toEqual({ label, family, icon: iconName });
  });

  it.each(['__proto__', 'constructor', 'toString'])(
    'shows %j, which names no OS, as given',
    (osType) => {
      expect(osTypeInfo(osType)).toEqual({
        label: osType,
        family: 'other',
        icon: 'desktop',
      });
    },
  );

  it.each([undefined, null, '', '  '])('gives nothing for %j', (osType) => {
    expect(osTypeInfo(osType)).toBeNull();
  });

  it('gives every named value a family and its icon, whatever its case', () => {
    fc.assert(
      fc.property(fc.string(), fc.boolean(), (text, upper) => {
        const info = osTypeInfo(text);
        if (!text.trim()) {
          expect(info).toBeNull();
          return;
        }
        expect(FAMILIES).toContain(info.family);
        expect(info.label.length).toBeGreaterThan(0);
        const cased = upper ? text.toUpperCase() : text.toLowerCase();
        expect(osTypeInfo(cased).family).toBe(info.family);
      }),
    );
  });
});

describe('the OS tag colors', () => {
  const style = readFileSync(
    join(
      import.meta.dirname,
      '..',
      'src/components/experiment/VmDetailsCard.vue',
    ),
    'utf8',
  );

  it.each(FAMILIES)('give the %s family text of 4.5:1 or more', (family) => {
    const rule = new RegExp(
      `\\.vm-os-tag\\.is-os-${family}\\s*\\{([^}]*)\\}`,
    ).exec(style)?.[1];
    expect(rule).toBeDefined();
    const background = /background-color:\s*(#[0-9a-f]+)/i.exec(rule)[1];
    const text = /(?:^|\s)color:\s*(#[0-9a-f]+)/i.exec(rule)[1];
    expect(contrast(fullHex(background), fullHex(text))).toBeGreaterThanOrEqual(
      4.5,
    );
  });
});

describe('the OS icons', () => {
  it.each([
    [faLinux, 'linux'],
    [faWindows, 'windows'],
  ])('are solid icons Font Awesome can draw by name', (definition, name) => {
    library.add(definition);
    const svg = icon({ prefix: 'fas', iconName: name });
    expect(svg.html.join('')).toMatch(/<svg[^>]*viewBox="0 0 512 512"/);
  });
});
