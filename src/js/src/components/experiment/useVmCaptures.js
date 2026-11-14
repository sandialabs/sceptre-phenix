import { getCurrentInstance, ref } from 'vue';
import { useRoute } from 'vue-router';

import * as experimentsApi from '@/api/experiments.js';
import * as vmsApi from '@/api/vms.js';
import { captureFilename, isInterfaceCapturing } from '@/utils/captures.js';
import { showError, useErrorNotification } from '@/utils/errorNotif';
import { plural } from '@/utils/plural.js';
import { uniqueStamp } from './timestamp.js';
import { captureTargets, vlanAlias } from './vmActions.js';

// How long after a bulk capture request the page still shows no toast for
// its captures' websocket events, as its summary toast tells of them.
export const QUIET_MS = 5000;

// Packet captures of the running experiment's VMs: starting and stopping them
// on the server, and which capture buttons are waiting on it. Call it from
// setup(); `fanOut` sends one request per name and summarizes how they went.
// `experimentOf(vm)` names the experiment of a VM; by default it is the
// experiment of the page route.
export function useVmCaptures({ fanOut, experimentOf }) {
  const { proxy } = getCurrentInstance();
  const route = useRoute();
  const exp = (vm) => experimentOf?.(vm) ?? route.params.id;

  // "experiment/vm/interface" keys of the capture buttons waiting on the server
  const capturePending = ref({});
  // "experiment/vm" keys of the VMs starting or stopping captures on all
  // their interfaces
  const captureAllPending = ref({});
  // aliases of the VLANs starting or stopping captures
  const vlanCapturePending = ref({});

  const captureKey = (vmName, iface) => `${vmName}/${iface}`;
  const vmKey = (vm) => `${exp(vm)}/${vm.name}`;
  const pendingKey = (vm, iface) => `${vmKey(vm)}/${iface}`;

  // the capture events of this page's bulk requests, which show no toast
  const quiet = new Set();

  // Hushes the events of the action that `matches(vm, iface, row)` picks
  // while a bulk request waits. The function it returns, called once the
  // server answers, hushes those that `then` picks for QUIET_MS more.
  function hush(action, matches) {
    const entry = { action, matches, until: Infinity };
    quiet.add(entry);
    return (then = matches) => {
      if (entry.until !== Infinity) return;
      entry.matches = then;
      entry.until = Date.now() + QUIET_MS;
    };
  }

  // Whether a capture's websocket event about the VM `name`, one of the
  // table's rows `vms` or not, is of this page's own bulk request.
  function quietCaptureEvent(msg, name, vms) {
    const iface = msg.result?.interface;
    const row = vms?.find((vm) => vm.name === name);
    const now = Date.now();
    for (const entry of quiet) {
      if (entry.until <= now) quiet.delete(entry);
      else if (
        entry.action === msg.resource.action &&
        entry.matches(name, iface, row)
      ) {
        return true;
      }
    }
    return false;
  }

  function setCapturePending(vm, iface, pending) {
    const key = pendingKey(vm, iface);
    if (pending) {
      capturePending.value = { ...capturePending.value, [key]: true };
    } else {
      const { [key]: _, ...rest } = capturePending.value;
      capturePending.value = rest;
    }
  }

  function setAllPending(vm, pending) {
    const key = vmKey(vm);
    const { [key]: _, ...rest } = captureAllPending.value;
    captureAllPending.value = pending ? { ...rest, [key]: true } : rest;
  }

  // whether the VM starts or stops captures on all its interfaces
  const isAllPending = (vm) => !!captureAllPending.value[vmKey(vm)];

  // the interfaces of a VM whose capture buttons are waiting
  function pendingCaptureIfaces(vm) {
    const prefix = `${vmKey(vm)}/`;
    return Object.keys(capturePending.value)
      .filter((key) => key.startsWith(prefix))
      .map((key) => Number(key.slice(prefix.length)));
  }

  // The capture button of an interface: asks the server which captures
  // are running (minimega lists them across the cluster, which takes a
  // moment), then starts or stops this interface's capture. The button
  // spins until the server has answered both. With a format (pcapng or
  // pcap), picked in the format dialog, it only starts one in that format.
  // With `startOnly`, it only starts one, as an interface picker asks.
  async function handlePcap(vm, iface, format, { startOnly = false } = {}) {
    if (capturePending.value[pendingKey(vm, iface)]) return;

    const time = uniqueStamp();
    const expName = exp(vm);

    setCapturePending(vm, iface, true);
    try {
      const resp = await vmsApi.listVmCaptures(expName, vm.name);
      // the row updates from the capture's websocket event
      const capturing = isInterfaceCapturing(resp.data?.captures ?? [], iface);
      if (capturing && (format || startOnly)) {
        proxy.$buefy.toast.open({
          message: `phēnix already captures interface ${iface} of the ${vm.name} VM.`,
          type: 'is-info',
        });
      } else if (capturing) {
        await vmsApi.deleteVmCapture(expName, vm.name, iface);
      } else if (vm.networks[iface] == 'disconnected') {
        showError('Cannot capture traffic on a disconnected interface.');
      } else {
        await vmsApi.createVmCapture(
          expName,
          vm.name,
          iface,
          captureFilename(vm.name, iface, time, format),
          format,
        );
      }
    } catch (err) {
      useErrorNotification(err);
    } finally {
      setCapturePending(vm, iface, false);
    }
  }

  // Starts a packet capture on every connected interface of a VM that
  // has none running, as the capture button does for one: the files are
  // named <vm>_<interface>_<stamp>.pcap. Its buttons spin while the server
  // is asked and while the captures start.
  async function captureAllInterfaces(vm) {
    if (isAllPending(vm)) return;

    const time = uniqueStamp();
    const expName = exp(vm);

    setAllPending(vm, true);
    let release;
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
      const keys = new Set(targets.map((iface) => captureKey(vm.name, iface)));
      release = hush('start', (name, iface) =>
        keys.has(captureKey(name, iface)),
      );
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
      release?.();
      setAllPending(vm, false);
    }
  }

  // Stops every packet capture running on a VM, from its stop all button,
  // which spins until the server answers.
  async function stopAllCaptures(vm) {
    if (isAllPending(vm)) return;

    setAllPending(vm, true);
    try {
      // the row updates from the captures' websocket events
      await vmsApi.deleteVmCaptures(exp(vm), vm.name);
    } catch (err) {
      useErrorNotification(err);
    } finally {
      setAllPending(vm, false);
    }
  }

  // Starts or stops the packet captures on every interface on a VLAN, from
  // an interface's network menu, starting them in the format when given;
  // the rows update from the captures' events. Its summary toast stands for
  // the events', which the server sends before it answers: those of a VM on
  // the VLAN, or not on this page, are hushed.
  async function captureVlan(alias, start, format) {
    if (vlanCapturePending.value[alias]) return;

    const vlan = alias.toLowerCase();
    const onVlan = (_, iface, row) =>
      !row || vlanAlias(row.networks?.[iface])?.toLowerCase() === vlan;
    const release = hush(start ? 'start' : 'stop', onVlan);

    vlanCapturePending.value = { ...vlanCapturePending.value, [alias]: true };
    try {
      if (start) {
        const resp = await experimentsApi.startVlanCaptures(
          route.params.id,
          alias,
          format,
        );
        const started = new Set(
          (resp.data?.captures ?? []).map((c) => captureKey(c.vm, c.interface)),
        );
        release((name, iface) => started.has(captureKey(name, iface)));
        const count = started.size;
        proxy.$buefy.toast.open({
          message: `Started ${plural(count, 'packet capture')} on the ${alias} VLAN.`,
          type: count ? 'is-success' : 'is-info',
        });
      } else {
        const resp = await experimentsApi.stopVlanCaptures(
          route.params.id,
          alias,
        );
        const vms = new Set(resp.data?.vms ?? []);
        release((name, ...rest) => vms.has(name) && onVlan(name, ...rest));
        proxy.$buefy.toast.open({
          message: `Stopped the packet captures on the ${alias} VLAN.`,
          type: 'is-success',
        });
      }
    } catch (err) {
      release(() => false);
      useErrorNotification(err);
    } finally {
      const { [alias]: _, ...rest } = vlanCapturePending.value;
      vlanCapturePending.value = rest;
    }
  }

  return {
    capturePending,
    captureAllPending,
    isAllPending,
    vlanCapturePending,
    captureVlan,
    quietCaptureEvent,
    pendingCaptureIfaces,
    handlePcap,
    captureAllInterfaces,
    stopAllCaptures,
  };
}
