import { describe, expect, test } from 'vitest';
import {
  applyStopCaptureUpdate,
  isInterfaceCapturing,
  hasMultipleCaptures,
} from '@/utils/captures.js';

describe('applyStopCaptureUpdate', () => {
  const captures = [
    { vm: 'test-vm', interface: 0, filename: 'iface0.pcap' },
    { vm: 'test-vm', interface: 1, filename: 'iface1.pcap' },
  ];

  test('removes only the specified interface, leaving others untouched', () => {
    expect(applyStopCaptureUpdate(captures, { interface: 0 })).toEqual([
      { vm: 'test-vm', interface: 1, filename: 'iface1.pcap' },
    ]);
  });

  test('clears all captures when no result is provided (stop-all)', () => {
    expect(applyStopCaptureUpdate(captures, undefined)).toEqual([]);
  });

  test('clears all captures when result has no interface field', () => {
    expect(applyStopCaptureUpdate(captures, {})).toEqual([]);
  });

  test('handles an empty/nil captures list without throwing', () => {
    expect(applyStopCaptureUpdate(null, { interface: 0 })).toEqual([]);
    expect(applyStopCaptureUpdate(undefined, { interface: 0 })).toEqual([]);
    expect(applyStopCaptureUpdate([], { interface: 0 })).toEqual([]);
  });

  test('treats interface 0 as a valid, present value (not falsy/missing)', () => {
    // Regression guard: interface index 0 must not be confused with "no
    // interface field present" just because 0 is falsy.
    expect(
      applyStopCaptureUpdate(
        [{ vm: 'test-vm', interface: 0, filename: 'iface0.pcap' }],
        { interface: 0 },
      ),
    ).toEqual([]);
  });
});

describe('isInterfaceCapturing', () => {
  const captures = [
    { vm: 'test-vm', interface: 0, filename: 'iface0.pcap' },
    { vm: 'test-vm', interface: 1, filename: 'iface1.pcap' },
  ];

  test('returns true when the interface has a running capture', () => {
    expect(isInterfaceCapturing(captures, 1)).toBe(true);
  });

  test('returns false when the interface has no running capture', () => {
    expect(isInterfaceCapturing(captures, 2)).toBe(false);
  });

  test('returns false for an empty/nil captures list', () => {
    expect(isInterfaceCapturing([], 0)).toBe(false);
    expect(isInterfaceCapturing(null, 0)).toBe(false);
    expect(isInterfaceCapturing(undefined, 0)).toBe(false);
  });

  test('treats interface 0 as a valid, present value', () => {
    expect(isInterfaceCapturing(captures, 0)).toBe(true);
  });
});

describe('hasMultipleCaptures', () => {
  test('returns false when zero or one captures are running', () => {
    expect(hasMultipleCaptures([])).toBe(false);
    expect(hasMultipleCaptures(null)).toBe(false);
    expect(hasMultipleCaptures(undefined)).toBe(false);
    expect(hasMultipleCaptures([{ interface: 0 }])).toBe(false);
  });

  test('returns true when more than one capture is running', () => {
    expect(hasMultipleCaptures([{ interface: 0 }, { interface: 1 }])).toBe(
      true,
    );
  });
});
