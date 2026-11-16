// The VM tools that the running experiment page and the console page share:
// a screenshot saved in the experiment files, and the state of the CD-ROM,
// port forward, memory snapshot, disk image and redeploy dialogs.
import { listDisks } from '@/api/disks.js';
import { saveVmScreenshot } from '@/api/vms.js';
import { baseName, isoDisks } from '@/utils/diskChain.js';
import { useErrorNotification } from '@/utils/errorNotif';
import { escapeHTML } from '@/utils/escapeHTML.js';
import { roleAllowed } from '@/utils/rbac.js';
import { timestampParts } from './timestamp.js';

// Saves a full-size screenshot of the VM in the experiment files, then says
// in a toast which file holds it, or shows the error. Resolves to the path.
export async function saveScreenshot(buefy, exp, vm) {
  try {
    const { data } = await saveVmScreenshot(exp, vm);
    buefy?.toast.open({
      message: escapeHTML(`Saved a screenshot of the ${vm} VM as ${data.path}`),
      type: 'is-success',
    });
    return data.path;
  } catch (err) {
    useErrorNotification(err);
    return null;
  }
}

// the CD-ROM picker with nothing open
export function emptyOpticalDiscModal() {
  return {
    active: false,
    vmName: null,
    // the ISO inserted when it opened, '' for none
    current: '',
    // the ISO picked
    disc: null,
    // the ISO images to pick from, while loading and after
    isos: [],
    loading: false,
    error: null,
  };
}

// the CD-ROM picker opened for a VM, its ISO image picked
export function openOpticalDiscModal(vm) {
  return {
    ...emptyOpticalDiscModal(),
    active: true,
    vmName: vm.name,
    current: vm.cdRom || '',
    disc: vm.cdRom || null,
  };
}

// Lists the ISO images that the picker can insert, or says why it cannot.
// It leaves the modal alone once stillOpen() is false.
export async function loadIsoChoices(modal, stillOpen) {
  if (!roleAllowed('disks', 'list')) {
    modal.error =
      'Your role cannot list disk images, so it cannot pick an ISO ' +
      'image to insert.';
    return;
  }

  modal.loading = true;
  let isos = [];
  let error = null;
  try {
    const resp = await listDisks({ diskType: 'ISO' });
    isos = isoDisks(resp.data?.disks);
  } catch (err) {
    error = 'Could not list the ISO images.';
    useErrorNotification(err);
  }

  if (!stillOpen()) return;
  modal.isos = isos;
  modal.error = error;
  modal.loading = false;
}

// the port forward dialog with nothing open
export function emptyPortForwardModal() {
  return {
    active: false,
    vmName: null,
    srcPort: null,
    dstHost: '127.0.0.1',
    dstPort: null,
  };
}

// The memory snapshot dialog's entry for a VM: its file is named after the
// VM and the time now, with no extension.
export function memorySnapshotEntry(name) {
  const t = timestampParts();
  const date = `${t.year}${t.month}${t.day}`;
  const time = `${t.hours}${t.minutes}${t.seconds}`;
  return {
    dateTime: date + time,
    name,
    filename: `${name}_${date}_${time}`,
    nameErrType: '',
    nameErrMsg: '',
  };
}

// The disk image dialog's entry for a VM: its file is named after the VM,
// the stem of its disk and the time now, with no extension.
export function diskImageEntry(vm) {
  const t = timestampParts();
  const stamp = `${t.year}${t.month}${t.day}${t.hours}${t.minutes}${t.seconds}`;
  // the disk's file name up to its first "_" when it holds a date and time,
  // else up to its first "."
  const file = baseName(vm.disk);
  const stem = file.split(/_\d{14}/.test(file) ? '_' : '.')[0];
  return {
    dateTime: stamp,
    name: vm.name,
    filename: `${vm.name}_${stem}_${stamp}`,
    nameErrType: '',
    nameErrMsg: '',
  };
}

// the redeploy dialog's entry for a VM, with its settings now
export function redeployEntry(vm) {
  return {
    name: vm.name,
    cpus: vm.cpus,
    ram: vm.ram,
    disk: vm.disk,
    // the server keeps the VM's disk unless another one is picked
    currentDisk: vm.disk,
    inject: false,
  };
}

// The redeploy request body for an entry of the redeploy dialog.
export function redeployBody(entry) {
  const body = { cpus: parseInt(entry.cpus), ram: parseInt(entry.ram) };
  // a snapshot VM's disk is the image under phēnix's own snapshot overlay;
  // sending it back would replace the overlay with the image
  if (entry.disk !== entry.currentDisk) body.disk = entry.disk;
  // the select stores the string 'true' or 'false'
  if (entry.inject === true || entry.inject === 'true') body.injects = true;
  return body;
}

// Whether every file name of the filename dialog is allowed; marks the first
// that is not with its error.
export function validFilenames(modal) {
  const allowed = /^[a-zA-Z0-9-_]+$/;
  for (const entry of modal.vm) {
    if (!allowed.test(entry.filename)) {
      entry.nameErrType = 'is-danger';
      entry.nameErrMsg =
        'image names can only contain alphanumeric, dash, and underscore; we will add the file extension';
      return false;
    }
    entry.nameErrType = '';
    entry.nameErrMsg = '';
  }
  return true;
}
