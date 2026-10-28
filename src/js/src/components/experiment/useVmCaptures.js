import { getCurrentInstance, ref } from 'vue';
import { useRoute } from 'vue-router';

import * as vmsApi from '@/api/vms.js';
import {
  captureFilename,
  hasMultipleCaptures,
  isInterfaceCapturing,
} from '@/utils/captures.js';
import { showError, useErrorNotification } from '@/utils/errorNotif';
import { listText, pluralWord } from '@/utils/plural.js';
import { minuteStamp } from './timestamp.js';
import { captureTargets } from './vmActions.js';

// Packet captures of the running experiment's VMs: starting and stopping them
// on the server, and which capture buttons are waiting on it. Call it from
// setup(); `setWaiting` shows the page's waiting overlay and `fanOut` sends
// one request per name and summarizes how they went.
export function useVmCaptures({ setWaiting, fanOut }) {
  const { proxy } = getCurrentInstance();
  const route = useRoute();
  const exp = () => route.params.id;

  // "vm/interface" keys of the capture buttons waiting on the server
  const capturePending = ref({});
  // names of the VMs starting or stopping captures on all their interfaces
  const captureAllPending = ref({});

  const captureKey = (vmName, iface) => `${vmName}/${iface}`;

  function setCapturePending(vmName, iface, pending) {
    const key = captureKey(vmName, iface);
    if (pending) {
      capturePending.value = { ...capturePending.value, [key]: true };
    } else {
      const { [key]: _, ...rest } = capturePending.value;
      capturePending.value = rest;
    }
  }

  function setAllPending(vmName, pending) {
    const { [vmName]: _, ...rest } = captureAllPending.value;
    captureAllPending.value = pending ? { ...rest, [vmName]: true } : rest;
  }

  // the interfaces of a VM whose capture buttons are waiting
  function pendingCaptureIfaces(vmName) {
    const prefix = `${vmName}/`;
    return Object.keys(capturePending.value)
      .filter((key) => key.startsWith(prefix))
      .map((key) => Number(key.slice(prefix.length)));
  }

  // Stops the capture on one interface of a VM, or with no `iface` all of
  // its captures; the row updates from the capture's websocket event.
  function stopVMCapture(vm, iface) {
    setWaiting(true);
    vmsApi
      .deleteVmCapture(exp(), vm.name, iface)
      .then(() => setWaiting(false))
      .catch((err) => {
        useErrorNotification(err);
        setWaiting(false);
      });
  }

  function startVMCapture(vm, iface, time) {
    setWaiting(true);
    vmsApi
      .createVmCapture(
        exp(),
        vm.name,
        iface,
        captureFilename(vm.name, iface, time),
      )
      .then(() => setWaiting(false))
      .catch((err) => {
        useErrorNotification(err);
        setWaiting(false);
      });
  }

  // The capture button of an interface: asks the server which captures
  // are running (minimega lists them across the cluster, which takes a
  // moment), then offers to start or stop this interface's. The button
  // shows a spinner until the dialog opens.
  function handlePcap(vm, iface) {
    if (capturePending.value[captureKey(vm.name, iface)]) return;

    const time = minuteStamp();

    setCapturePending(vm.name, iface, true);
    vmsApi
      .listVmCaptures(exp(), vm.name)
      .finally(() => setCapturePending(vm.name, iface, false))
      .then((response) => {
        const captures = response.data.captures || [];
        const capturing = isInterfaceCapturing(captures, iface);

        if (capturing && hasMultipleCaptures(captures)) {
          // let the user stop just this interface's capture or all of them
          proxy.$buefy.dialog.confirm({
            title: 'Stop Packet Capture',
            message:
              'The ' +
              vm.name +
              ' VM has multiple packet captures running. Stop the ' +
              'capture on interface ' +
              iface +
              ' only, or stop all packet captures for the VM?',
            cancelText: 'Stop Interface ' + iface + ' Only',
            confirmText: 'Stop All Captures',
            type: 'is-danger',
            hasIcon: true,
            onConfirm: () => stopVMCapture(vm),
            onCancel: (trigger) => {
              // escape or a click outside the dialog stops nothing
              if (trigger === 'button') stopVMCapture(vm, iface);
            },
          });
        } else if (capturing) {
          proxy.$buefy.dialog.confirm({
            title: 'Stop Packet Capture',
            message:
              'This will stop the packet capture on interface ' +
              iface +
              ' for the ' +
              vm.name +
              ' VM.',
            cancelText: 'Cancel',
            confirmText: 'Stop',
            type: 'is-danger',
            hasIcon: true,
            onConfirm: () => stopVMCapture(vm, iface),
          });
        } else if (vm.networks[iface] == 'disconnected') {
          showError('Cannot capture traffic on a disconnected interface.');
        } else {
          proxy.$buefy.dialog.confirm({
            title: 'Start a Packet Capture',
            message:
              'This will start a packet capture for the ' +
              vm.name +
              ' VM, interface ' +
              iface +
              '.',
            cancelText: 'Cancel',
            confirmText: 'Start',
            type: 'is-success',
            hasIcon: true,
            onConfirm: () => startVMCapture(vm, iface, time),
          });
        }
      })
      .catch((err) => {
        useErrorNotification(err);
      });
  }

  // Starts a packet capture on every connected interface of a VM that
  // has none running, as the capture button does for one: the files are
  // named <vm>_<interface>_<time>.pcap. The Actions column's button shows
  // a spinner while the server is asked and while the captures start.
  async function captureAllInterfaces(vm) {
    if (captureAllPending.value[vm.name]) return;

    const time = minuteStamp();
    const expName = exp();

    setAllPending(vm.name, true);
    let targets;
    try {
      const resp = await vmsApi.listVmCaptures(expName, vm.name);
      targets = captureTargets(vm, resp.data?.captures ?? []);
    } catch (err) {
      useErrorNotification(err);
      return;
    } finally {
      setAllPending(vm.name, false);
    }

    if (targets.length === 0) {
      proxy.$buefy.toast.open({
        message: `Every connected interface of the ${vm.name} VM is already being captured.`,
        type: 'is-info',
      });
      return;
    }

    const files = targets.map((iface) => captureFilename(vm.name, iface, time));
    proxy.$buefy.dialog.confirm({
      title: `Start ${pluralWord(targets.length, 'a Packet Capture', 'Packet Captures')}`,
      message:
        `This will start ${pluralWord(targets.length, 'a packet capture', 'packet captures')} ` +
        `on ${pluralWord(targets.length, 'interface')} ${listText(targets)} ` +
        `of the ${vm.name} VM, writing ${listText(files)} to the ` +
        "experiment's files.",
      cancelText: 'Cancel',
      confirmText: targets.length == 1 ? 'Start' : 'Start All',
      type: 'is-success',
      hasIcon: true,
      onConfirm: async () => {
        setAllPending(vm.name, true);
        // the rows update from the captures' websocket events
        const file = Object.fromEntries(
          targets.map((iface, i) => [`interface ${iface}`, files[i]]),
        );
        try {
          await fanOut(
            Object.keys(file),
            `Started packet captures of ${vm.name} on`,
            (name) =>
              vmsApi.createVmCapture(
                expName,
                vm.name,
                Number(name.split(' ')[1]),
                file[name],
              ),
            'interface',
          );
        } finally {
          setAllPending(vm.name, false);
        }
      },
    });
  }

  // Stops every packet capture running on a VM, from the Actions
  // column's stop button; it spins until the server answers.
  function stopAllCaptures(vm) {
    if (captureAllPending.value[vm.name]) return;

    const ifaces = [...new Set((vm.captures ?? []).map((c) => c.interface))];
    proxy.$buefy.dialog.confirm({
      title: `Stop ${pluralWord(ifaces.length, 'Packet Capture', 'Packet Captures')}`,
      message:
        `This will stop the packet ${pluralWord(ifaces.length, 'capture')} ` +
        `on ${pluralWord(ifaces.length, 'interface')} ${listText(ifaces)} ` +
        `of the ${vm.name} VM.`,
      cancelText: 'Cancel',
      confirmText: ifaces.length == 1 ? 'Stop' : 'Stop All',
      type: 'is-danger',
      hasIcon: true,
      onConfirm: async () => {
        setAllPending(vm.name, true);
        try {
          // the row updates from the captures' websocket events
          await vmsApi.deleteVmCaptures(exp(), vm.name);
        } catch (err) {
          useErrorNotification(err);
        } finally {
          setAllPending(vm.name, false);
        }
      },
    });
  }

  return {
    capturePending,
    captureAllPending,
    pendingCaptureIfaces,
    handlePcap,
    stopVMCapture,
    captureAllInterfaces,
    stopAllCaptures,
  };
}
