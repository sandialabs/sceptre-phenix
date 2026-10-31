import { getCurrentInstance, ref } from 'vue';
import { useRoute } from 'vue-router';

import * as vmsApi from '@/api/vms.js';
import { captureFilename, isInterfaceCapturing } from '@/utils/captures.js';
import { showError, useErrorNotification } from '@/utils/errorNotif';
import { uniqueStamp } from './timestamp.js';
import { captureTargets } from './vmActions.js';

// Packet captures of the running experiment's VMs: starting and stopping them
// on the server, and which capture buttons are waiting on it. Call it from
// setup(); `fanOut` sends one request per name and summarizes how they went.
export function useVmCaptures({ fanOut }) {
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

  // The capture button of an interface: asks the server which captures
  // are running (minimega lists them across the cluster, which takes a
  // moment), then starts or stops this interface's capture. The button
  // spins until the server has answered both.
  async function handlePcap(vm, iface) {
    if (capturePending.value[captureKey(vm.name, iface)]) return;

    const time = uniqueStamp();
    const expName = exp();

    setCapturePending(vm.name, iface, true);
    try {
      const resp = await vmsApi.listVmCaptures(expName, vm.name);
      // the row updates from the capture's websocket event
      if (isInterfaceCapturing(resp.data?.captures ?? [], iface)) {
        await vmsApi.deleteVmCapture(expName, vm.name, iface);
      } else if (vm.networks[iface] == 'disconnected') {
        showError('Cannot capture traffic on a disconnected interface.');
      } else {
        await vmsApi.createVmCapture(
          expName,
          vm.name,
          iface,
          captureFilename(vm.name, iface, time),
        );
      }
    } catch (err) {
      useErrorNotification(err);
    } finally {
      setCapturePending(vm.name, iface, false);
    }
  }

  // Starts a packet capture on every connected interface of a VM that
  // has none running, as the capture button does for one: the files are
  // named <vm>_<interface>_<stamp>.pcap. Its buttons spin while the server
  // is asked and while the captures start.
  async function captureAllInterfaces(vm) {
    if (captureAllPending.value[vm.name]) return;

    const time = uniqueStamp();
    const expName = exp();

    setAllPending(vm.name, true);
    try {
      const resp = await vmsApi.listVmCaptures(expName, vm.name);
      const targets = captureTargets(vm, resp.data?.captures ?? []);
      if (targets.length === 0) {
        proxy.$buefy.toast.open({
          message: `phēnix already captures every connected interface of the ${vm.name} VM.`,
          type: 'is-info',
        });
        return;
      }

      // the rows update from the captures' websocket events
      const file = Object.fromEntries(
        targets.map((iface) => [
          `interface ${iface}`,
          captureFilename(vm.name, iface, time),
        ]),
      );
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
    } catch (err) {
      useErrorNotification(err);
    } finally {
      setAllPending(vm.name, false);
    }
  }

  // Stops every packet capture running on a VM, from its stop all button,
  // which spins until the server answers.
  async function stopAllCaptures(vm) {
    if (captureAllPending.value[vm.name]) return;

    setAllPending(vm.name, true);
    try {
      // the row updates from the captures' websocket events
      await vmsApi.deleteVmCaptures(exp(), vm.name);
    } catch (err) {
      useErrorNotification(err);
    } finally {
      setAllPending(vm.name, false);
    }
  }

  return {
    capturePending,
    captureAllPending,
    pendingCaptureIfaces,
    handlePcap,
    captureAllInterfaces,
    stopAllCaptures,
  };
}
