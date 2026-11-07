// How many of the experiment's VMs still wait for a delayed start: the VMs the
// last VM list named, kept current by the VM events since.

// whether a VM as a row or an event shows it has left minimega's BUILDING
// state, that is, it started at least once
function started(vm) {
  return !!vm?.state && vm.state !== 'BUILDING';
}

export function createDelayedVms() {
  // the names of the VMs that wait, or null until a list names them
  let waiting = null;

  return {
    // A VM list's waiting VMs (null without them), and the table's rows,
    // which keep the VMs changed since it was read.
    listed(names, rows) {
      if (!Array.isArray(names)) {
        waiting = null;
        return null;
      }

      waiting = new Set(names);
      for (const row of rows ?? []) {
        if (started(row)) waiting.delete(row.name);
      }
      return waiting.size;
    },

    // An experiment/vm event about the VM `name`, carrying the VM as `result`
    // when it has one; returns the count after it.
    changed(name, action, result) {
      if (waiting === null) return null;

      if (action === 'delete' || started(result)) waiting.delete(name);
      else if (result?.state === 'BUILDING' && result.delayed_start) {
        waiting.add(name);
      }
      return waiting.size;
    },
  };
}
