// The bar of an experiment start or stop (components/ExperimentProgress.vue):
// what it shows, what screen readers get, and a failure in its place.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import ExperimentProgress from '@/components/ExperimentProgress.vue';
import { mountComponent, textOf } from './helpers/mount.js';

const render = (props) =>
  mountComponent(ExperimentProgress, { props: { name: 'demo', ...props } });

// what the bar draws and what a screen reader gets from it
const read = (wrapper) => {
  const bar = wrapper.find('progress');
  const label = wrapper.find('.exp-progress-label');
  return {
    value: bar.exists() ? bar.element.value : null,
    shown: textOf(wrapper.find('.progress-value')),
    label: label.exists() ? textOf(label) : '',
    name: bar.attributes('aria-label') ?? null,
    valuetext: bar.attributes('aria-valuetext') ?? null,
  };
};
const live = (wrapper) => textOf(wrapper.find('[aria-live="polite"]'));

describe('the bar', () => {
  it('shows the percent and the phase, also to screen readers', async () => {
    const wrapper = await render({
      progress: {
        operation: 'start',
        percent: 0.42,
        phase: 'pre-start-apps',
        done: 3,
        total: 7,
        step: 'startup',
      },
    });

    expect(read(wrapper)).toEqual({
      value: 42,
      shown: '42%',
      label: 'Pre-start apps 3/7: startup',
      name: 'Progress of the start of experiment demo',
      valuetext: 'Pre-start apps 3/7: startup',
    });
    expect(wrapper.find('.notification').exists()).toBe(false);
  });

  it("shows only the bar for an old server's percent", async () => {
    const wrapper = await render({ progress: { percent: 0.5 } });

    expect(read(wrapper)).toEqual({
      value: 50,
      shown: '50%',
      label: '',
      name: 'Progress of experiment demo',
      valuetext: null,
    });
    expect(live(wrapper)).toBe('');
  });

  it('shows 0 with no progress yet', async () => {
    const wrapper = await render({ progress: null });
    expect(read(wrapper).value).toBe(0);
  });

  it('keeps aria-valuetext with the label as it changes', async () => {
    const wrapper = await render({
      progress: { operation: 'stop', phase: 'stop-vms', done: 1, total: 4 },
    });

    await wrapper.setProps({
      progress: { operation: 'stop', phase: 'stop-vms', done: 2, total: 4 },
    });
    expect(read(wrapper).valuetext).toBe('Stopping VMs 2/4');
    expect(read(wrapper).name).toBe('Progress of the stop of experiment demo');

    await wrapper.setProps({ progress: { operation: 'stop', percent: 0.9 } });
    expect(read(wrapper).valuetext).toBe(null);
  });
});

describe('the announcements', () => {
  const at = (phase, done, step = '') => ({
    operation: 'start',
    percent: done / 10,
    phase,
    done,
    total: 10,
    step,
  });

  it('tell a new phase, not a new count', async () => {
    const wrapper = await render({ progress: at('launch-vms', 1) });
    expect(live(wrapper)).toBe('Launching VMs 1/10');

    await wrapper.setProps({ progress: at('launch-vms', 2) });
    expect(read(wrapper).label).toBe('Launching VMs 2/10');
    expect(live(wrapper)).toBe('Launching VMs 1/10');

    await wrapper.setProps({ progress: at('start-vms', 3) });
    expect(live(wrapper)).toBe('Starting VMs 3/10');
  });

  it('change only when the phase does, for any run of messages', async () => {
    const wrapper = await render({ progress: at('prepare', 0) });
    await fc.assert(
      fc.asyncProperty(
        fc.array(
          fc.record({
            phase: fc.constantFrom(
              'launch-vms',
              'start-vms',
              'post-start-apps',
            ),
            done: fc.nat(10),
            step: fc.constantFrom('', 'ntp', 'scorch'),
          }),
          { minLength: 1, maxLength: 8 },
        ),
        async (messages) => {
          let phase = wrapper.props('progress').phase;
          let heard = live(wrapper);
          for (const msg of messages) {
            await wrapper.setProps({
              progress: at(msg.phase, msg.done, msg.step),
            });
            if (msg.phase !== phase) heard = read(wrapper).label;
            phase = msg.phase;
            expect(live(wrapper)).toBe(heard);
          }
        },
      ),
      { numRuns: 20 },
    );
  });
});

describe('a failure', () => {
  it('replaces the bar with the reason, in the danger color', async () => {
    const wrapper = await render({
      progress: { operation: 'start', phase: 'launch-vms', percent: 0.3 },
      failure: {
        operation: 'start',
        phase: 'launch-vms',
        step: 'vm-3',
        error: 'no image',
      },
    });

    const box = wrapper.find('.notification');
    expect(box.classes()).toContain('is-danger');
    expect(textOf(box)).toBe('Failed at Launching VMs: vm-3: no image');
    expect(wrapper.find('progress').exists()).toBe(false);
    expect(live(wrapper)).toBe('Failed at Launching VMs: vm-3: no image');
  });
});
