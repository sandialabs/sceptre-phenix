import { describe, expect, test } from 'vitest';
import fc from 'fast-check';

import {
  fitRoute,
  handleOffsetY,
  routePath,
  simplifyRoute,
} from '@/builder/routes.js';

// Every stretch runs across or down.
const orthogonal = (points) =>
  points
    .slice(1)
    .every(
      (point, index) =>
        point.x === points[index].x || point.y === points[index].y,
    );

describe('connection routes', () => {
  test('repeated points and points midway along a stretch are left out', () => {
    expect(
      simplifyRoute([
        { x: 0, y: 0 },
        { x: 0, y: 0 },
        { x: 10, y: 0 },
        { x: 20, y: 0 },
        { x: 20, y: 30 },
      ]),
    ).toEqual([
      { x: 0, y: 0 },
      { x: 20, y: 0 },
      { x: 20, y: 30 },
    ]);
  });

  test('a route moves onto new ends, its bends following, still across and down', () => {
    const route = [
      { x: 100, y: 50 },
      { x: 120, y: 50 },
      { x: 120, y: 200 },
      { x: 180, y: 200 },
    ];
    const fitted = fitRoute(route, { x: 104, y: 56 }, { x: 180, y: 192 });

    expect(fitted).toEqual([
      { x: 104, y: 56 },
      { x: 120, y: 56 },
      { x: 120, y: 192 },
      { x: 180, y: 192 },
    ]);
    // A straight line whose ends no longer line up steps halfway.
    const stepped = fitRoute(
      [
        { x: 0, y: 40 },
        { x: 100, y: 40 },
      ],
      { x: 0, y: 40 },
      { x: 100, y: 56 },
    );

    expect(stepped).toEqual([
      { x: 0, y: 40 },
      { x: 50, y: 40 },
      { x: 50, y: 56 },
      { x: 100, y: 56 },
    ]);
    expect(orthogonal(fitted) && orthogonal(stepped)).toBe(true);
    // An end moved further than allowed: the route no longer fits.
    expect(fitRoute(route, { x: 110, y: 50 }, route[3], 4)).toBeNull();
    expect(fitRoute(route, { x: 102, y: 51 }, route[3], 4)).not.toBeNull();
    expect(fitRoute(undefined, route[0], route[3])).toBeNull();
    expect(fitRoute([route[0]], route[0], route[3])).toBeNull();
  });

  test('a route is drawn with rounded bends, its label halfway along', () => {
    const drawn = routePath(
      [
        { x: 0, y: 0 },
        { x: 40, y: 0 },
        { x: 40, y: 100 },
        { x: 100, y: 100 },
      ],
      8,
    );

    expect(drawn.path).toBe('M0 0L32 0Q40 0 40 8L40 92Q40 100 48 100L100 100');
    // 200 long: halfway is 60 down the middle stretch.
    expect([drawn.labelX, drawn.labelY]).toEqual([40, 60]);
    // A bend is never rounder than half its shorter stretch.
    expect(
      routePath([
        { x: 0, y: 0 },
        { x: 4, y: 0 },
        { x: 4, y: 20 },
      ]).path,
    ).toBe('M0 0L2 0Q4 0 4 2L4 20');
  });

  test('a connection meets a device at its interface and anything else halfway down', () => {
    const device = {
      kind: 'device',
      size: { width: 160, height: 96 },
      device: {
        interfaces: [
          { id: 'a', name: 'eth0', index: 0 },
          { id: 'b', name: 'eth1', index: 1 },
        ],
      },
    };

    expect(handleOffsetY(device, 'a')).toBe(32);
    expect(handleOffsetY(device, 'b')).toBe(64);
    expect(handleOffsetY({ kind: 'switch' }, 'bus')).toBe(36);
  });
});

const cell = fc.record({
  x: fc.integer({ min: 0, max: 3 }),
  y: fc.integer({ min: 0, max: 3 }),
});
const smallRoute = fc.array(cell, { minLength: 1, maxLength: 8 });
const spot = fc.record({
  x: fc.integer({ min: -8, max: 8 }),
  y: fc.integer({ min: -8, max: 8 }),
});

// A route of stretches across or down, some doubling back or of no length.
const orthogonalRoute = fc
  .array(fc.tuple(fc.boolean(), fc.integer({ min: -5, max: 5 })), {
    minLength: 1,
    maxLength: 6,
  })
  .map((steps) => {
    const points = [{ x: 0, y: 0 }];

    for (const [across, d] of steps) {
      const last = points[points.length - 1];

      points.push(
        across ? { x: last.x + d, y: last.y } : { x: last.x, y: last.y + d },
      );
    }

    return points;
  });

const samePoint = (a, b) => a.x === b.x && a.y === b.y;

describe('connection route properties', () => {
  test('a route doubling back on itself keeps no repeated point', () => {
    expect(
      simplifyRoute([
        { x: 3, y: 1 },
        { x: 3, y: 0 },
        { x: 3, y: 1 },
        { x: 0, y: 0 },
      ]),
    ).toEqual([
      { x: 3, y: 1 },
      { x: 0, y: 0 },
    ]);
  });

  test('simplifying twice changes nothing, and no point repeats the one before', () => {
    fc.assert(
      fc.property(smallRoute, (points) => {
        const once = simplifyRoute(points);

        expect(simplifyRoute(once)).toEqual(once);
        expect(
          once.slice(1).every((point, i) => !samePoint(point, once[i])),
        ).toBe(true);
      }),
      { numRuns: 300 },
    );
  });

  test('a fitted route runs from start to end across and down, and refits to itself', () => {
    fc.assert(
      fc.property(orthogonalRoute, spot, spot, (points, start, end) => {
        // Coincident ends collapse a route to one point, which no longer fits.
        fc.pre(!samePoint(start, end));
        const fitted = fitRoute(points, start, end);

        expect(fitted[0]).toEqual(start);
        expect(fitted.at(-1)).toEqual(end);
        expect(orthogonal(fitted)).toBe(true);
        expect(fitRoute(fitted, start, end)).toEqual(fitted);
      }),
      { numRuns: 300 },
    );
  });
});
