// How many of the experiment's VMs still wait for a delayed start, and when
// those that wait for a timer start: the VMs the last VM list named, kept
// current by the VM events since.

// whether a VM as a row or an event shows it has left minimega's BUILDING
// state, that is, it started at least once
function started(vm) {
  return !!vm?.state && vm.state !== 'BUILDING';
}

export function createDelayedVms() {
  // the names of the VMs that wait, or null until a list names them
  let waiting = null;
  // the due time (RFC 3339) of each waiting VM that waits for a timer
  let due = {};

  return {
    // A VM list's waiting VMs (null without them), the table's rows, which
    // keep the VMs changed since it was read, and the list's due times.
    listed(names, rows, dueTimes) {
      if (!Array.isArray(names)) {
        waiting = null;
        due = {};
        return null;
      }

      waiting = new Set(names);
      for (const row of rows ?? []) {
        if (started(row)) waiting.delete(row.name);
      }
      due = Object.fromEntries(
        Object.entries(dueTimes ?? {}).filter(([name]) => waiting.has(name)),
      );
      return waiting.size;
    },

    // An experiment/vm event about the VM `name`, carrying the VM as `result`
    // when it has one; returns the count after it.
    changed(name, action, result) {
      if (waiting === null) return null;

      // a redeployed VM no longer waits for the timer of the start
      if (action === 'redeployed') delete due[name];
      if (action === 'delete' || started(result)) {
        waiting.delete(name);
        delete due[name];
      } else if (result?.state === 'BUILDING' && result.delayed_start) {
        waiting.add(name);
      }
      return waiting.size;
    },

    // a copy of the due times by VM name of the waiting VMs that have one
    dueTimes() {
      return { ...due };
    },
  };
}
