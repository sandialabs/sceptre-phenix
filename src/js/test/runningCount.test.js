// The running experiment's count of running VMs: what a VM list says, and
// how the VM events since change it.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import { createRunningCount } from '@/components/experiment/runningCount.js';

const vm = (name, running) => ({ name, running });

describe('the running count', () => {
  it('is unknown until a list counts the running VMs', () => {
    const count = createRunningCount();
    expect(count.changed('a', 'start', vm('a', true))).toBeNull();
    expect(count.listed(undefined, [vm('a', true)], [vm('a', true)])).toBe(
      null,
    );
    expect(count.listed(3, [vm('a', true)], [vm('a', true)])).toBe(3);
  });

  it('follows the rows kept from changes and kills newer than the list', () => {
    const count = createRunningCount();
    const listed = [vm('a', true), vm('b', false), vm('c', true)];
    // a stopped since the list was read, c killed
    const rows = [vm('a', false), vm('b', false)];
    expect(count.listed(5, listed, rows)).toBe(3);
  });

  it.each([
    ['a shown VM shut down', 'a', 'shutdown', vm('a', false), 1],
    ['a shown VM started again', 'a', 'start', vm('a', true), 2],
    ['a shown stopped VM killed', 'b', 'delete', null, 2],
    ['a VM not shown started', 'x', 'start', vm('x', true), 3],
    ['a VM not shown paused', 'x', 'stop', vm('x', false), 1],
    ['a VM not shown killed', 'x', 'delete', null, 1],
    ['a VM not shown updated', 'x', 'update', vm('x', true), 2],
    ['an event without a VM', 'a', 'cdrom-inserted', undefined, 2],
  ])('counts %s', (_, name, action, result, want) => {
    const count = createRunningCount();
    count.listed(
      2,
      [vm('a', true), vm('b', false)],
      [vm('a', true), vm('b', false)],
    );
    expect(count.changed(name, action, result)).toBe(want);
  });

  // A VM is started only while stopped and paused, shut down or killed only
  // while running; an update may change a VM the page knows of.
  it('counts the running VMs through any run of events', () => {
    const vms = fc.uniqueArray(
      fc.record({
        name: fc.constantFrom('a', 'b', 'c', 'd', 'e', 'f'),
        running: fc.boolean(),
        shown: fc.boolean(),
      }),
      { selector: (v) => v.name, maxLength: 6 },
    );
    const steps = fc.array(
      fc.record({ pick: fc.nat(), choice: fc.nat(), flip: fc.boolean() }),
      { maxLength: 20 },
    );
    fc.assert(
      fc.property(vms, steps, (start, events) => {
        const model = new Map(start.map((v) => [v.name, v.running]));
        const known = new Set(start.filter((v) => v.shown).map((v) => v.name));
        const shown = start
          .filter((v) => v.shown)
          .map((v) => vm(v.name, v.running));
        const running = () => [...model.values()].filter(Boolean).length;

        const count = createRunningCount();
        expect(count.listed(running(), shown, shown)).toBe(running());

        for (const { pick, choice, flip } of events) {
          const names = [...model.keys()];
          if (names.length === 0) break;
          const name = names[pick % names.length];
          const was = model.get(name);
          const actions = was
            ? ['stop', 'shutdown', 'delete', 'update']
            : ['start', 'update'];
          const action = actions[choice % actions.length];

          let now = was;
          if (action === 'start') now = true;
          else if (action !== 'update') now = false;
          else if (known.has(name) && flip) now = !was;

          if (action === 'delete') model.delete(name);
          else model.set(name, now);
          known.add(name);

          const result = action === 'delete' ? null : vm(name, now);
          expect(count.changed(name, action, result)).toBe(running());
        }
      }),
    );
  });
});
