// How the VM details card names a VM's guest operating system and its icon.
import { icon, library } from '@fortawesome/fontawesome-svg-core';
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { osTypeInfo } from '@/components/experiment/osType.js';
import { faLinux, faWindows } from '@/utils/icons.js';

const FAMILIES = ['linux', 'windows', 'router', 'other'];

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
