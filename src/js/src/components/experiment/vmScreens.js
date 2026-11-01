import externalImg from '@/assets/imgs/external.png';
import notAvailableImg from '@/assets/imgs/not-available.png';
import delayedImg from '@/assets/imgs/delayed.png';
import notRunningImg from '@/assets/imgs/not-running.png';
import loadingScreenshotImg from '@/assets/imgs/loading-screenshot.svg';
import { usePhenixStore } from '@/store';

// The picture of a running experiment's VM that its VM table, VNC tiles and
// details show, and the link to its VNC console. `due` says whether VMs still
// without a screenshot have waited long enough for one.

// the image to show for a VM: its screenshot, or a picture of why it has none
export function vmScreenshot(vm, due) {
  if (vm.external) {
    return externalImg;
  } else if (vm.running && !vm.busy && !vm.screenshot) {
    // the server sends screenshots after the VM list
    return due ? notAvailableImg : loadingScreenshotImg;
  } else if (vm.delayed_start && vm.state == 'BUILDING') {
    return delayedImg;
  } else if (vm.running && !vm.busy && vm.screenshot) {
    return vm.screenshot;
  } else if (vm.busy) {
    return notAvailableImg;
  } else {
    return notRunningImg;
  }
}

// Alt text for vmScreenshot's image, which for a VM without a screenshot is a
// picture of a message; keep the branches the same.
export function screenshotAlt(vm, due) {
  const name = vm.name;
  if (vm.external) {
    return `VM ${name} is an external node`;
  } else if (vm.running && !vm.busy && !vm.screenshot) {
    return due
      ? `Screenshot of VM ${name} is not available`
      : `Loading the screenshot of VM ${name}`;
  } else if (vm.delayed_start && vm.state == 'BUILDING') {
    return `VM ${name} has a delayed start`;
  } else if (vm.running && !vm.busy && vm.screenshot) {
    return `Screenshot of VM ${name}`;
  } else if (vm.busy) {
    return `Screenshot of VM ${name} is not available while it is busy`;
  } else {
    return `VM ${name} is not running`;
  }
}

// Alt text for a screenshot that links to the VM's console: where the link
// goes, then what the picture says when it isn't a screenshot.
export function consoleLinkAlt(vm, due) {
  const link = `Open the console of VM ${vm.name}`;
  const alt = screenshotAlt(vm, due);
  return alt == `Screenshot of VM ${vm.name}` ? link : `${link}: ${alt}`;
}

// the URL of a VM's VNC console in an experiment
export function vncHref(router, experiment, vm) {
  return router.resolve({
    name: 'vnc',
    params: {
      id: experiment,
      name: vm.name,
      token: usePhenixStore().token,
    },
  }).href;
}
