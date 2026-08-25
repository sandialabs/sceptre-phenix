import { describe, expect, test } from 'vitest';
import { partitionSnapshotVMNames } from '@/utils/vmSnapshot.js';

describe('partitionSnapshotVMNames', () => {
  const vms = [
    { name: 'enabled', snapshot: true },
    { name: 'disabled', snapshot: false },
    { name: 'unset' },
  ];

  test('partitions VM names by snapshot support', () => {
    expect(
      partitionSnapshotVMNames(
        ['disabled', 'enabled', 'unset', 'unknown'],
        vms,
      ),
    ).toEqual({
      enabled: ['enabled'],
      disabled: ['disabled', 'unset', 'unknown'],
    });
  });

  test('accepts a single VM name', () => {
    expect(partitionSnapshotVMNames('enabled', vms)).toEqual({
      enabled: ['enabled'],
      disabled: [],
    });
  });
});
