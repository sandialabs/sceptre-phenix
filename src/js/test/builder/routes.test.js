import { describe, expect, test } from 'vitest';

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
