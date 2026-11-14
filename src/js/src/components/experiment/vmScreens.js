import loadingScreenshotImg from '@/assets/imgs/loading-screenshot.svg';
import { osTypeInfo } from './osType.js';
import { placeholderImage } from './vmPlaceholder.js';

// The picture of a running experiment's VM that its VM table, VNC tiles and
// details show. `due` says whether VMs still without a screenshot have waited
// long enough for one.

// What vmScreenshot shows for a VM: 'screenshot', 'loading', or the kind of
// placeholder picture (see vmPlaceholder.js). Keep the alt text the same.
function screenKind(vm, due) {
  if (vm.external) return 'external';
  if (vm.running && !vm.busy && !vm.screenshot)
    // the server sends screenshots after the VM list
    return due ? 'notAvailable' : 'loading';
  if (vm.delayed_start && vm.state == 'BUILDING') return 'delayed';
  if (vm.running && !vm.busy && vm.screenshot) return 'screenshot';
  if (vm.busy) return 'busy';
  return vm.dnb ? 'doNotBoot' : 'notRunning';
}

// the image to show for a VM: its screenshot, or a picture of why it has none
export function vmScreenshot(vm, due) {
  const kind = screenKind(vm, due);
  if (kind === 'screenshot') return vm.screenshot;
  if (kind === 'loading') return loadingScreenshotImg;
  const detail =
    kind === 'delayed' && typeof vm.delayed_start === 'string'
      ? `starts after ${vm.delayed_start}`
      : undefined;
  return placeholderImage(kind, { osType: vm.os_type, detail });
}

const ALT = {
  external: (name) => `VM ${name} is an external node`,
  notAvailable: (name) => `Screenshot of VM ${name} is not available`,
  loading: (name) => `Loading the screenshot of VM ${name}`,
  delayed: (name) => `VM ${name} has a delayed start`,
  screenshot: (name) => `Screenshot of VM ${name}`,
  busy: (name) => `Screenshot of VM ${name} is not available while it is busy`,
  doNotBoot: (name) => `VM ${name} is set to not boot`,
  notRunning: (name) => `VM ${name} is not running`,
};

// Alt text for vmScreenshot's image; a placeholder also names the guest OS.
export function screenshotAlt(vm, due) {
  const kind = screenKind(vm, due);
  const alt = ALT[kind](vm.name);
  const os = osTypeInfo(vm.os_type);
  return os && kind !== 'screenshot' && kind !== 'loading'
    ? `${alt} (${os.label})`
    : alt;
}

// Alt text for a screenshot that links to the VM's console: where the link
// goes, then what the picture says when it isn't a screenshot.
export function consoleLinkAlt(vm, due) {
  const link = `Open the console of VM ${vm.name}`;
  const alt = screenshotAlt(vm, due);
  return alt == `Screenshot of VM ${vm.name}` ? link : `${link}: ${alt}`;
}
