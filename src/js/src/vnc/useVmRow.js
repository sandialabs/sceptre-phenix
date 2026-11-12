// The VM row of one console pane, for its state tag and its VM actions. It
// loads the VM once and follows it on the event stream. The VNC session
// gets the VM state from the event stream too (see useVncSession.js). The
// packet captures of the row come again from GET captures when one starts
// or stops.
import {
  getCurrentInstance,
  getCurrentScope,
  onBeforeUnmount,
  onScopeDispose,
  shallowRef,
} from 'vue';

import { getVm, listVmCaptures } from '@/api/vms.js';
import {
  addWsHandler,
  onWsReconnect,
  removeWsHandler,
} from '@/utils/websocket';

// experiment/vm actions that a busy VM sends before the action runs
const BUSY = new Set(['starting', 'stopping', 'restarting', 'redeploying']);

/**
 * The VM row of `experiment`/`name`, as GET /experiments/{exp}/vms/{vm}
 * answers it. It is null until it loads, and null again when the VM is
 * gone. Call it in setup or in an effect scope.
 *
 * @param {string} experiment
 * @param {string} name
 * @returns {{ vm: import('vue').ShallowRef<object|null>,
 *   reload: () => Promise<void>, reloadCaptures: () => Promise<void>,
 *   dispose: () => void }}
 */
export function useVmRow(experiment, name) {
  const vm = shallowRef(null);
  let generation = 0;
  let disposed = false;

  async function reload() {
    if (disposed) return;
    const mine = ++generation;
    try {
      const { data } = await getVm(experiment, name);
      if (mine === generation) vm.value = data;
    } catch (err) {
      if (mine !== generation) return;
      if (err?.response?.status === 404) vm.value = null;
      else console.warn(`failed to load the VM ${experiment}/${name}`, err);
    }
  }

  // the running captures of the VM, into the row that has loaded
  async function reloadCaptures() {
    if (disposed) return;
    try {
      const { data } = await listVmCaptures(experiment, name);
      if (vm.value && !disposed) {
        vm.value = { ...vm.value, captures: data?.captures ?? [] };
      }
    } catch (err) {
      if (err?.response?.status !== 404) {
        console.warn(
          `failed to list the captures of ${experiment}/${name}`,
          err,
        );
      }
    }
  }

  function handleWsMessage(msg) {
    const resource = msg?.resource;
    if (resource?.type === 'experiment' && resource.name === experiment) {
      if (resource.action === 'stop') vm.value = null;
      return;
    }
    if (
      resource?.type === 'experiment/vm/capture' &&
      resource.name === `${experiment}/${name}`
    ) {
      reloadCaptures();
      return;
    }
    if (
      resource?.type !== 'experiment/vm' ||
      resource.name !== `${experiment}/${name}`
    ) {
      return;
    }
    const result = msg.result;
    if (result?.name === name && 'running' in result) {
      generation += 1;
      vm.value = result;
    } else if (BUSY.has(resource.action) && vm.value) {
      vm.value = { ...vm.value, busy: true };
    } else {
      reload();
    }
  }

  addWsHandler(handleWsMessage);
  const stopReconnect = onWsReconnect(reload);
  reload().then(reloadCaptures);

  function dispose() {
    if (disposed) return;
    disposed = true;
    generation += 1;
    removeWsHandler(handleWsMessage);
    stopReconnect();
  }

  if (getCurrentInstance()) onBeforeUnmount(dispose);
  else if (getCurrentScope()) onScopeDispose(dispose);

  return { vm, reload, reloadCaptures, dispose };
}
