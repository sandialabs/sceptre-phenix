export function partitionSnapshotVMNames(names, vms) {
  const requestedNames = Array.isArray(names) ? names : [names];
  const snapshotEnabled = new Map(vms.map((vm) => [vm.name, vm.snapshot]));
  const enabled = [];
  const disabled = [];

  requestedNames.forEach((name) => {
    if (snapshotEnabled.get(name) === true) {
      enabled.push(name);
    } else {
      disabled.push(name);
    }
  });

  return { enabled, disabled };
}
