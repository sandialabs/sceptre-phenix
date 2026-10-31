// How many of the experiment's VMs are running: the count the last VM list
// gave, kept current by the VM events since.

// Whether a VM unheard of since the list was running before an event; events
// not named here change no count.
const RUNNING_BEFORE = {
  start: false,
  stop: true,
  shutdown: true,
  delete: true,
};

export function createRunningCount() {
  // whether each VM was running when last heard of, by name
  const states = new Map();
  let count = null;

  return {
    // A VM list's count (null without one) and VMs, and the table's rows made
    // from them, which keep the VMs changed or removed since it was read.
    listed(running, listed, rows) {
      states.clear();
      if (typeof running !== 'number') {
        count = null;
        return count;
      }

      count = running;
      const shown = new Map((rows ?? []).map((vm) => [vm.name, vm]));
      for (const vm of listed ?? []) {
        const row = shown.get(vm.name);
        const now = !!row?.running;
        if (row) states.set(vm.name, now);
        if (!!vm.running !== now) count += now ? 1 : -1;
      }
      count = Math.max(count, 0);
      return count;
    },

    // An experiment/vm event about the VM `name`, carrying the VM as `result`
    // when it has one; returns the count after it.
    changed(name, action, result) {
      if (count === null) return count;

      let now;
      if (action === 'delete') now = false;
      else if (typeof result?.running === 'boolean') now = result.running;
      else return count;

      const was = states.has(name)
        ? states.get(name)
        : (RUNNING_BEFORE[action] ?? now);
      states.set(name, now);
      count = Math.max(count + (now ? 1 : 0) - (was ? 1 : 0), 0);
      return count;
    },
  };
}
