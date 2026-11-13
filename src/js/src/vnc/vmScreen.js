// Whether a VM has a screen that a console or a live tile can show, and
// whether the role may open it. One rule for the tiles, the details modal and
// their console links.
import { roleAllowed } from '@/utils/rbac.js';

/**
 * Whether the VM is paused. A paused VM keeps its screen.
 *
 * @param {{ state?: string }} vm the VM row
 */
export function vmPaused(vm) {
  return (vm?.state ?? '').toUpperCase() === 'PAUSED';
}

/**
 * Whether the VM has a screen to show: it runs or is paused, and it is not
 * busy with another action.
 *
 * @param {{ running?: boolean, state?: string, busy?: boolean }} vm the VM row
 */
function vmHasScreen(vm) {
  return Boolean(vm) && (Boolean(vm.running) || vmPaused(vm)) && !vm.busy;
}

/**
 * Whether the role may see the screen of the VM now, live or on the console
 * page: the VM has a screen and the role has `vms/vnc` get.
 *
 * @param {string} experiment
 * @param {{ name: string, running?: boolean, state?: string, busy?: boolean }} vm
 */
export function consoleAvailable(experiment, vm) {
  return (
    vmHasScreen(vm) && roleAllowed('vms/vnc', 'get', `${experiment}/${vm.name}`)
  );
}
