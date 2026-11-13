// The banners of a console pane (T37): the server's lines are plain text in
// its colors, and a banner with no lines draws nothing.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import VncBanner from '@/vnc/VncBanner.vue';
import { mountComponent } from './helpers/mount.js';

const banner = (lines, fields = {}) => ({
  lines,
  backgroundColor: 'white',
  textColor: 'black',
  ...fields,
});

const mountBanner = (props) => mountComponent(VncBanner, { props });

const lineTexts = (wrapper) =>
  wrapper.findAll('.vnc-banner-line').map((line) => line.element.textContent);

describe('a console banner', () => {
  it('draws each line in the colors of the banner', async () => {
    const wrapper = await mountBanner({
      banner: banner(['UNCLASSIFIED', 'EXP: demo - VM: web'], {
        backgroundColor: 'green',
        textColor: 'rgb(255, 255, 255)',
      }),
      position: 'top',
    });
    expect(lineTexts(wrapper)).toEqual(['UNCLASSIFIED', 'EXP: demo - VM: web']);
    const root = wrapper.find('.vnc-banner');
    expect(root.classes()).toContain('vnc-banner--top');
    expect(root.element.style.backgroundColor).toBe('green');
    expect(root.element.style.color).toBe('rgb(255, 255, 255)');
  });

  it.each([
    ['no lines', banner([])],
    ['no banner', null],
  ])('draws nothing for %s', async (_, value) => {
    const wrapper = await mountBanner({ banner: value, position: 'bottom' });
    expect(wrapper.find('.vnc-banner').exists()).toBe(false);
  });

  it('shows any line exactly as the server sent it', async () => {
    await fc.assert(
      fc.asyncProperty(
        fc.array(fc.string({ unit: 'binary' }), { minLength: 1, maxLength: 3 }),
        async (lines) => {
          const wrapper = await mountBanner({ banner: banner(lines) });
          expect(lineTexts(wrapper)).toEqual(lines);
          expect(wrapper.find('.vnc-banner-line *').exists()).toBe(false);
          wrapper.unmount();
        },
      ),
      {
        numRuns: 8,
        // markup must stay text (main bug #17)
        examples: [
          [
            [
              '<img src=x onerror="alert(1)">',
              '<b>SECRET</b> &amp; <script>alert(2)</script>',
            ],
          ],
        ],
      },
    );
  });
});
