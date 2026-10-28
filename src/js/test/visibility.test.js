// trackVisibility: which of a page's items are on screen, as an
// IntersectionObserver reports them.
import fc from 'fast-check';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { trackVisibility } from '@/utils/visibility.js';

// An IntersectionObserver whose reports the test makes: report(el, visible)
// tells the observer watching el, as a browser would once el moves.
class FakeObserver {
  static all = [];

  constructor(callback) {
    this.callback = callback;
    this.watched = new Set();
    FakeObserver.all.push(this);
  }

  observe(el) {
    this.watched.add(el);
  }

  unobserve(el) {
    this.watched.delete(el);
  }

  disconnect() {
    this.watched.clear();
  }

  static report(el, visible) {
    for (const observer of FakeObserver.all) {
      if (observer.watched.has(el)) {
        observer.callback([{ target: el, isIntersecting: visible }]);
      }
    }
  }
}

const useFakeObserver = () => {
  FakeObserver.all = [];
  vi.stubGlobal('IntersectionObserver', FakeObserver);
};

afterEach(() => vi.unstubAllGlobals());

describe('trackVisibility', () => {
  it('tells of items as they come on screen, once each time', () => {
    useFakeObserver();
    const shown = [];
    const tiles = trackVisibility((keys) => shown.push(...keys));
    const [a, b] = [{}, {}];

    tiles.track('a', a);
    tiles.track('b', b);
    expect(tiles.shownKeys()).toEqual([]);

    FakeObserver.report(a, true);
    FakeObserver.report(a, true);
    expect(shown).toEqual(['a']);
    expect(tiles.isShown('a')).toBe(true);
    expect(tiles.isShown('b')).toBe(false);

    FakeObserver.report(a, false);
    FakeObserver.report(a, true);
    expect(shown).toEqual(['a', 'a']);

    // a re-render passes the same element again, which changes nothing
    tiles.track('a', a);
    expect(tiles.isShown('a')).toBe(true);

    // an unmounted item is no longer watched
    tiles.track('a', null);
    FakeObserver.report(a, true);
    expect(tiles.shownKeys()).toEqual([]);
    expect(shown).toEqual(['a', 'a']);
  });

  it('counts every item as on screen without an IntersectionObserver', () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    const shown = [];
    const tiles = trackVisibility((keys) => shown.push(...keys));

    tiles.track('a', {});
    expect(shown).toEqual(['a']);
    expect(tiles.isShown('a')).toBe(true);
  });

  it('reports as shown exactly the items whose current element was last seen on screen', () => {
    const key = fc.constantFrom('a', 'b', 'c');
    const op = fc.oneof(
      fc.record({
        track: key,
        el: fc.constantFrom(null, 0, 1),
      }),
      fc.record({
        report: key,
        el: fc.constantFrom(0, 1),
        visible: fc.boolean(),
      }),
    );

    fc.assert(
      fc.property(fc.array(op, { maxLength: 30 }), (ops) => {
        useFakeObserver();
        const elements = new Map();
        const elementOf = (k, i) => {
          const id = `${k}${i}`;
          if (!elements.has(id)) elements.set(id, { id });
          return elements.get(id);
        };

        let calls = 0;
        const tiles = trackVisibility((keys) => (calls += keys.length));

        // the model: each key's tracked element, and whether it is shown
        const tracked = new Map();
        const model = new Set();
        let appeared = 0;

        for (const o of ops) {
          if ('track' in o) {
            const el = o.el === null ? null : elementOf(o.track, o.el);
            if (tracked.get(o.track) !== el) {
              model.delete(o.track);
              if (el) tracked.set(o.track, el);
              else tracked.delete(o.track);
            }
            tiles.track(o.track, el);
          } else {
            const el = elementOf(o.report, o.el);
            if (tracked.get(o.report) === el) {
              if (!o.visible) model.delete(o.report);
              else if (!model.has(o.report)) {
                model.add(o.report);
                appeared++;
              }
            }
            FakeObserver.report(el, o.visible);
          }
        }

        expect(tiles.shownKeys().sort()).toEqual([...model].sort());
        expect(calls).toBe(appeared);
      }),
    );
  });
});
