import { onBeforeUnmount } from 'vue';

import { applyStopCaptureUpdate } from '@/utils/captures.js';
import { onWsReconnect } from '@/utils/websocket';

// The running experiment page's VM table rows as the websocket changes them:
// the VM lists the page asks for, merged with the per-VM events that arrive
// around them, and the toast each event shows.

// experiment/vm messages that change or remove a VM's row, whose seq a VM
// list read earlier must not undo (screenshots and errors change no state)
const VM_CHANGES = new Set([
  'update',
  'delete',
  'start',
  'stop',
  'redeploying',
  'redeployed',
]);

// The seq of the last change to each VM, and of each VM's delete, from the
// websocket, so a VM list the server read before a change does not undo it.
export function createVmSeqs() {
  const changed = new Map();
  const deleted = new Map();

  return {
    // forgets every change, as a restarted server numbers its messages afresh
    clear() {
      changed.clear();
      deleted.clear();
    },

    // records the seq of a websocket message that changed or deleted a VM
    record(name, msg) {
      const action = msg.resource.action;
      if (msg.seq === undefined || !VM_CHANGES.has(action)) return;

      const seqs = action === 'delete' ? deleted : changed;
      seqs.set(name, Math.max(seqs.get(name) ?? -Infinity, msg.seq));
      if (action !== 'delete') deleted.delete(name);
    },

    // The VMs of a list the server read at seq listSeq, as the table shows
    // them: a VM changed by a websocket message after the list was read keeps
    // the row on screen (from `current`), one deleted since stays out, and
    // the screenshots already shown are kept, as they follow the list. A list
    // without a seq loses to every recorded change.
    listed(current, vms, listSeq) {
      const seq = listSeq ?? -Infinity;
      const shownRows = new Map((current ?? []).map((vm) => [vm.name, vm]));

      const rows = vms
        .filter((vm) => !((deleted.get(vm.name) ?? -Infinity) > seq))
        .map((vm) => {
          const shown = shownRows.get(vm.name);
          if (shown && (changed.get(vm.name) ?? -Infinity) > seq) {
            return shown;
          }
          return { ...vm, screenshot: vm.screenshot || shown?.screenshot };
        });

      // changes this list already reflects need no remembering
      if (Number.isFinite(seq)) {
        for (const seqs of [changed, deleted]) {
          for (const [name, at] of seqs) if (at <= seq) seqs.delete(name);
        }
      }

      return rows;
    },
  };
}

// createVmSeqs for a component, forgetting its changes whenever the websocket
// reconnects until the component unmounts; call it from setup().
export function useVmRows() {
  const seqs = createVmSeqs();
  const off = onWsReconnect(() => seqs.clear());
  onBeforeUnmount(off);
  return seqs;
}

// The VM a per-VM event is about, or null when the event is for another
// experiment than `id` or the VM table has not loaded.
export function eventVmName(msg, id, experiment) {
  const [exp, name] = (msg.resource.name ?? '').split('/');
  if (exp !== id || !experiment.vms) return null;
  return name ?? null;
}

export function findVm(experiment, name) {
  return experiment.vms.find((vm) => vm.name == name);
}

// Swaps in the server's copy of a VM. A copy without a screenshot keeps the
// one shown while the VM stays running; a VM that stopped, or is starting
// again, has no current screen until the next one arrives.
export function replaceVm(experiment, vm) {
  const vms = experiment.vms;
  const i = vms.findIndex((v) => v.name == vm.name);
  if (i === -1) return;
  const old = vms[i];
  vms[i] = {
    ...vm,
    screenshot:
      vm.screenshot || (vm.running && old.running ? old.screenshot : ''),
  };
  experiment.vms = [...vms];
}

function setVmBusy(experiment, name, busy) {
  const row = findVm(experiment, name);
  if (!row) return;
  row.busy = busy;
  if (busy) row.percent = 0;
}

function setVmPercent(experiment, name, fraction) {
  const row = findVm(experiment, name);
  if (!row) return;
  row.busy = true; // in case the starting message was missed
  row.percent = Math.round(fraction * 100);
}

// the toasts applyVmEvent gives; RunningExperiment shows a danger one as an
// error that stays until dismissed
const success = (message) => ({ message, type: 'is-success' });
const warning = (message) => ({ message, type: 'is-warning' });
const danger = (message) => ({ message, type: 'is-danger' });

// experiment/vm: the VM itself changed
function vmChange(experiment, msg, name) {
  switch (msg.resource.action) {
    case 'update':
      replaceVm(experiment, msg.result);
      return success(
        'The VM ' + msg.result.name + ' has been successfully updated.',
      );

    case 'delete': {
      const vms = experiment.vms;
      const i = vms.findIndex((v) => v.name == name);
      if (i !== -1) vms.splice(i, 1);
      return success('The ' + name + ' VM was killed.');
    }

    // also published when a delayed-start VM starts
    case 'start':
    case 'stop':
    case 'redeploying':
      replaceVm(experiment, msg.result);
      return null;

    case 'cdrom-inserted':
    case 'cdrom-ejected':
      return success(
        'The optical disc for ' +
          name +
          ' was successfully ' +
          (msg.resource.action == 'cdrom-inserted' ? 'inserted.' : 'ejected.'),
      );

    case 'redeployed':
      replaceVm(experiment, msg.result);
      return success('Redeployed ' + name);

    case 'errorRedeploying':
      setVmBusy(experiment, name, false);
      return danger('Redeploying the ' + name + ' VM failed.');

    case 'error':
      return danger(msg.result.error);
  }
  return null;
}

// experiment/vm/commit and experiment/vm/memorySnapshot: a backing image or
// memory snapshot of the VM being written
function vmCommit(experiment, msg, name, what) {
  switch (msg.resource.action) {
    case 'commit': {
      if (!findVm(experiment, name)) return null;
      if (what === 'backing image') {
        replaceVm(experiment, { ...msg.result.vm, busy: false });
      } else {
        setVmBusy(experiment, name, false);
      }
      const article = what === 'backing image' ? 'The' : 'A';
      return success(
        `${article} ${what} with name ` +
          msg.result.disk +
          ' for the ' +
          name +
          ' VM was successfully created.',
      );
    }

    case 'committing':
      if (!findVm(experiment, name)) return null;
      setVmBusy(experiment, name, true);
      return warning(
        `phēnix is creating a ${what} with name ` +
          msg.result.disk +
          ' for the ' +
          name +
          ' VM.',
      );

    case 'progress':
      setVmPercent(experiment, name, msg.result.percent);
      return null;

    case 'errorCommitting':
      setVmBusy(experiment, name, false);
      return danger(`Creating a ${what} for the ` + name + ' VM failed.');
  }
  return null;
}

// experiment/vm/capture: a packet capture started or stopped
function vmCapture(experiment, msg, name) {
  const row = findVm(experiment, name);

  switch (msg.resource.action) {
    case 'start':
      if (row) {
        if (row.captures == null) row.captures = [];
        row.captures.push({
          vm: name,
          interface: msg.result.interface,
          filename: msg.result.filename,
        });
      }
      return success(
        msg.result?.interface !== undefined
          ? `Packet capture was started on interface ${msg.result.interface} of the ${name} VM.`
          : `Packet capture was started for the ${name} VM.`,
      );

    case 'stop':
      if (row) row.captures = applyStopCaptureUpdate(row.captures, msg.result);
      return success(
        msg.result && msg.result.interface !== undefined
          ? 'Packet capture was stopped for interface ' +
              msg.result.interface +
              ' on the ' +
              name +
              ' VM.'
          : 'Packet capture was stopped for the ' + name + ' VM.',
      );
  }
  return null;
}

// experiment/vm/snapshot: a snapshot of the VM taken or restored
function vmSnapshot(experiment, msg, name) {
  if (!findVm(experiment, name)) return null;

  switch (msg.resource.action) {
    case 'create':
      setVmBusy(experiment, name, false);
      return success(
        'The snapshot for the ' + name + ' VM was successfully created.',
      );

    case 'creating':
      setVmBusy(experiment, name, true);
      return warning('phēnix is creating a snapshot for the ' + name + ' VM.');

    case 'progress':
      setVmPercent(experiment, name, msg.result.percent);
      return null;

    case 'restore':
      setVmBusy(experiment, name, false);
      return success(
        'The ' + name + ' VM was successfully reverted to a previous snapshot.',
      );

    case 'restoring':
      setVmBusy(experiment, name, true);
      return warning('phēnix is restoring a snapshot for the ' + name + ' VM.');

    case 'errorCreating':
    case 'errorRestoring':
      setVmBusy(experiment, name, false);
      return danger(
        (msg.resource.action == 'errorCreating'
          ? 'Creating a snapshot for the '
          : 'Restoring a snapshot for the ') +
          name +
          ' VM failed.',
      );
  }
  return null;
}

// the websocket message types applyVmEvent handles
export const VM_EVENT_TYPES = [
  'experiment/vm',
  'experiment/vm/commit',
  'experiment/vm/memorySnapshot',
  'experiment/vm/screenshot',
  'experiment/vm/capture',
  'experiment/vm/snapshot',
];

// Applies a websocket event about the VM `name` to the experiment's rows and
// returns the toast to show for it ({ message, type }), or null.
export function applyVmEvent(experiment, msg, name) {
  switch (msg.resource.type) {
    case 'experiment/vm':
      return vmChange(experiment, msg, name);
    case 'experiment/vm/commit':
      return vmCommit(experiment, msg, name, 'backing image');
    case 'experiment/vm/memorySnapshot':
      return vmCommit(experiment, msg, name, 'memory snapshot');
    case 'experiment/vm/screenshot':
      // Screenshots arrive for every subscribed VM every few seconds: mutate
      // the row in place instead of replacing the array, which would make
      // b-table re-process every row.
      if (msg.resource.action == 'update') {
        const row = findVm(experiment, name);
        if (row) row.screenshot = msg.result.screenshot;
      }
      return null;
    case 'experiment/vm/capture':
      return vmCapture(experiment, msg, name);
    case 'experiment/vm/snapshot':
      return vmSnapshot(experiment, msg, name);
  }
  return null;
}
