// The pictures of VMs with no screenshot: a state icon and label in the
// state's color, and the guest OS in a chip when the VM has an os_type.
import { describe, expect, it } from 'vitest';

import {
  PLACEHOLDERS,
  placeholderImage,
  placeholderSvg,
} from '@/components/experiment/vmPlaceholder.js';
import {
  screenshotAlt,
  vmScreenshot,
} from '@/components/experiment/vmScreens.js';

const svgOf = (url) =>
  decodeURIComponent(url.replace('data:image/svg+xml;charset=utf-8,', ''));

describe('placeholderSvg', () => {
  it.each(Object.entries(PLACEHOLDERS))(
    'draws %s in its color with its label',
    (kind, { color, label }) => {
      const svg = placeholderSvg(kind);
      expect(svg).toContain(`stroke="${color}"`);
      expect(svg).toContain(`>${label}</text>`);
      // no OS chip without an os_type
      expect(svg).not.toContain('<rect x="20"');
    },
  );

  it.each([
    ['windows', 'Windows'],
    ['VyOS', 'VyOS'],
    ['centos', 'CentOS'],
    ['plan9', 'plan9'],
  ])('names the os_type %s as %s in a chip', (osType, label) => {
    const svg = placeholderSvg('external', { osType });
    expect(svg).toContain('<rect x="20"');
    expect(svg).toContain(`>${label}</text>`);
  });

  it('escapes an os_type and a detail line', () => {
    const svg = placeholderSvg('delayed', {
      osType: '<b>&',
      detail: 'starts after "x"',
    });
    expect(svg).toContain('&lt;b&gt;&amp;');
    expect(svg).toContain('starts after &quot;x&quot;');
    expect(svg).not.toContain('<b>');
  });
});

describe('placeholderImage', () => {
  it('gives the same URL for the same picture', () => {
    const a = placeholderImage('busy', { osType: 'linux' });
    expect(placeholderImage('busy', { osType: 'linux' })).toBe(a);
    expect(placeholderImage('busy')).not.toBe(a);
    expect(svgOf(a)).toBe(placeholderSvg('busy', { osType: 'linux' }));
  });
});

describe('vmScreenshot and screenshotAlt', () => {
  it.each([
    ['an external node', { external: true }, 'external'],
    ['a stopped VM', { running: false }, 'notRunning'],
    ['a VM set not to boot', { running: false, dnb: true }, 'doNotBoot'],
    ['a busy VM', { running: true, busy: true }, 'busy'],
  ])('pictures %s with its OS', (_, fields, kind) => {
    const vm = { name: 'vm1', os_type: 'windows', ...fields };
    expect(vmScreenshot(vm, true)).toBe(
      placeholderImage(kind, { osType: 'windows' }),
    );
    expect(screenshotAlt(vm, true)).toMatch(/ \(Windows\)$/);
  });

  it('says when a delayed VM starts', () => {
    const vm = { name: 'vm1', delayed_start: '5m', state: 'BUILDING' };
    expect(svgOf(vmScreenshot(vm))).toContain('>starts after 5m</text>');
    expect(screenshotAlt(vm)).toBe('VM vm1 has a delayed start');
  });

  it('says that a VM set not to boot does not boot', () => {
    expect(screenshotAlt({ name: 'vm1', dnb: true })).toBe(
      'VM vm1 is set to not boot',
    );
  });

  it('keeps a screenshot and its alt text as they are', () => {
    const vm = {
      name: 'vm1',
      running: true,
      screenshot: 'data:image/png;base64,AA==',
      os_type: 'linux',
    };
    expect(vmScreenshot(vm)).toBe(vm.screenshot);
    expect(screenshotAlt(vm)).toBe('Screenshot of VM vm1');
  });
});
