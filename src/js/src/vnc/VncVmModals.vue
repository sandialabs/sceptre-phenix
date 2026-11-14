<!-- The dialogs of the VM actions of the console top bar that ask for more:
     the CD-ROM picker, a new port forward, the name of a memory snapshot or
     a disk image, the settings of a redeploy, the interface and the format
     of a packet capture, and the interface and the link quality to set.
     Most are the dialogs of the experiment page. The page opens
     one for the controller of a pane, whose pending action spins while the
     request runs. The page sends a redeploy and a capture itself. -->
<script setup>
  import { getCurrentInstance, ref } from 'vue';

  import { listDisks } from '@/api/disks.js';
  import {
    commitVmDisk,
    createVmForward,
    createVmMemorySnapshot,
    ejectVmDisc,
    insertVmDisc,
  } from '@/api/vms.js';
  import CaptureFormatModal from '@/components/experiment/CaptureFormatModal.vue';
  import OpticalDiscModal from '@/components/experiment/OpticalDiscModal.vue';
  import PortForwardModal from '@/components/experiment/PortForwardModal.vue';
  import RedeployModal from '@/components/experiment/RedeployModal.vue';
  import VmFilenamesModal from '@/components/experiment/VmFilenamesModal.vue';
  import VmLinkQualityModal from '@/components/experiment/VmLinkQualityModal.vue';
  import {
    MEMORY_SNAPSHOT_DOES,
    vmInterfaces,
  } from '@/components/experiment/vmActions.js';
  import {
    diskImageEntry,
    emptyOpticalDiscModal,
    emptyPortForwardModal,
    loadIsoChoices,
    memorySnapshotEntry,
    openOpticalDiscModal,
    redeployBody,
    redeployEntry,
    validFilenames,
  } from '@/components/experiment/vmTools.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import VncInterfaceModal from './VncInterfaceModal.vue';

  const props = defineProps({
    // minimega takes a capture format: the format dialog asks for one
    askFormat: { type: Boolean, default: false },
  });

  const emit = defineEmits([
    // (action, controller): the request of a dialog succeeded
    'done',
    // (controller, body): redeploy the VM with the settings picked
    'redeploy',
    // (controller, format, iface): start a capture on the interface in the
    // format, or on interface 0
    'capture',
  ]);

  const buefy = getCurrentInstance()?.proxy?.$buefy;

  const disc = ref(emptyOpticalDiscModal());
  const forward = ref(emptyPortForwardModal());
  const memory = ref({ active: false, vm: [] });
  const image = ref({ active: false, vm: [] });
  const redeploy = ref({ active: false, vm: [] });
  // the disk images the redeploy dialog offers, null until they load
  const disks = ref(null);
  const format = ref({ active: false, title: '', iface: 0 });
  // the interface picker, and what it picks the interface for
  const picker = ref({ active: false, title: '', for: null, interfaces: [] });
  const quality = ref({
    active: false,
    experiment: '',
    vm: '',
    index: 0,
    network: '',
  });
  // the controller that each dialog acts for
  let target = null;

  const toast = (message) =>
    buefy?.toast.open({ message: escapeHTML(message), type: 'is-success' });

  // Runs the request of an action on the target VM, its button spinning,
  // then says how it went.
  async function run(controller, action, send, done) {
    controller.pending = action;
    try {
      await send();
      toast(done);
      emit('done', action, controller);
    } catch (err) {
      useErrorNotification(err);
    } finally {
      controller.pending = null;
    }
  }

  /**
   * Opens the dialog of the action for the VM of the controller.
   *
   * @param {'cdrom'|'portForward'|'memorySnapshot'|'commit'|'redeploy'|
   *   'captureIface'|'captureInterface'|'linkQuality'} action
   * @param {{ experiment: string, vm: string, row: object|null,
   *   pending: string|null }} controller
   */
  async function open(action, controller) {
    target = controller;
    if (action === 'portForward') {
      forward.value = { ...emptyPortForwardModal(), active: true };
    } else if (action === 'memorySnapshot') {
      memory.value = { active: true, vm: [memorySnapshotEntry(controller.vm)] };
    } else if (action === 'commit') {
      image.value = {
        active: true,
        vm: [diskImageEntry({ ...controller.row, name: controller.vm })],
      };
    } else if (action === 'redeploy') {
      redeploy.value = {
        active: true,
        vm: [redeployEntry({ ...controller.row, name: controller.vm })],
      };
      loadDisks(controller.experiment);
    } else if (action === 'captureIface') {
      askCaptureFormat(0);
    } else if (action === 'captureInterface' || action === 'linkQuality') {
      pickInterface(action, controller);
    } else if (action === 'cdrom') {
      disc.value = openOpticalDiscModal({
        name: controller.vm,
        cdRom: controller.row?.cdRom,
      });
      const modal = disc.value;
      await loadIsoChoices(modal, () => disc.value === modal);
    }
  }

  // Asks for the interface, unless the VM has only one that the action can
  // use. A capture cannot use a disconnected or captured interface.
  function pickInterface(action, controller) {
    const capture = action === 'captureInterface';
    const interfaces = vmInterfaces(controller.row).map((iface) => ({
      index: iface.index,
      network: iface.network,
      ip: iface.ip,
      reason: !capture
        ? null
        : iface.disconnected
          ? 'The interface is disconnected.'
          : iface.capturing
            ? 'A capture of the interface runs.'
            : null,
    }));
    if (interfaces.length === 1 && !interfaces[0].reason) {
      interfacePicked(action, interfaces[0].index);
      return;
    }
    picker.value = {
      active: true,
      for: action,
      interfaces,
      title: capture
        ? `Start capture on an interface of ${controller.vm}`
        : `Set the link quality of an interface of ${controller.vm}`,
    };
  }

  function interfacePicked(action, index) {
    const controller = target;
    if (action === 'linkQuality') {
      quality.value = {
        active: true,
        experiment: controller.experiment,
        vm: controller.vm,
        index,
        network: (controller.row?.networks?.[index] ?? '').toLowerCase(),
      };
    } else if (props.askFormat) {
      askCaptureFormat(index);
    } else {
      emit('capture', controller, undefined, index);
    }
  }

  function askCaptureFormat(iface) {
    format.value = {
      active: true,
      iface,
      title: `Start capture on interface ${iface} of ${target.vm}`,
    };
  }

  // the disk images inside the minimega files directory and those the
  // experiment names outside it; none for a role that cannot list them
  async function loadDisks(experiment) {
    disks.value = null;
    if (!roleAllowed('disks', 'list')) return;
    try {
      const resp = await listDisks({ expName: experiment });
      disks.value = resp.data?.disks ?? [];
    } catch (err) {
      useErrorNotification(err);
    }
  }

  function createForward() {
    const controller = target;
    const { srcPort: src, dstHost: host, dstPort: dst } = forward.value;
    forward.value = emptyPortForwardModal();
    run(
      controller,
      'portForward',
      () =>
        createVmForward(controller.experiment, controller.vm, {
          src,
          host,
          dst,
        }),
      `Port forward ${src} → ${host}:${dst} created for the ${controller.vm} VM.`,
    );
  }

  function createMemorySnapshot() {
    const controller = target;
    const [entry] = memory.value.vm;
    memory.value = { active: false, vm: [] };
    run(
      controller,
      'memorySnapshot',
      () =>
        createVmMemorySnapshot(
          controller.experiment,
          controller.vm,
          `${entry.filename}.elf`,
        ),
      `Created a memory snapshot of the ${controller.vm} VM`,
    );
  }

  // asks, as the experiment page does, before it creates the disk image
  function createImage() {
    const controller = target;
    const [entry] = image.value.vm;
    image.value = { active: false, vm: [] };
    buefy?.dialog.confirm({
      title: 'Create a Disk Image',
      message: escapeHTML(
        `This will create a backing image of the ${controller.vm} VM.`,
      ),
      cancelText: 'Cancel',
      confirmText: 'Create',
      type: 'is-success',
      hasIcon: true,
      onConfirm: () =>
        run(
          controller,
          'commit',
          () =>
            commitVmDisk(
              controller.experiment,
              controller.vm,
              `${entry.filename}.qc2`,
            ),
          `Created a backing image of the ${controller.vm} VM`,
        ),
    });
  }

  function sendRedeploy() {
    const [entry] = redeploy.value.vm;
    redeploy.value = { active: false, vm: [] };
    emit('redeploy', target, redeployBody(entry));
  }

  function startCapture(picked) {
    const { iface } = format.value;
    format.value = { active: false, title: '', iface: 0 };
    emit('capture', target, picked, iface);
  }

  function insertDisc() {
    const controller = target;
    const isoPath = disc.value.disc;
    if (!isoPath) return;
    disc.value.active = false;
    run(
      controller,
      'cdrom',
      () => insertVmDisc(controller.experiment, controller.vm, isoPath),
      `Inserted ${isoPath} in the CD-ROM drive of the ${controller.vm} VM`,
    );
  }

  function ejectDisc() {
    const controller = target;
    disc.value.active = false;
    run(
      controller,
      'cdrom',
      () => ejectVmDisc(controller.experiment, controller.vm),
      `Ejected the CD-ROM of the ${controller.vm} VM`,
    );
  }

  defineExpose({ open });
</script>

<template>
  <PortForwardModal
    v-model:active="forward.active"
    v-model:src-port="forward.srcPort"
    v-model:dst-host="forward.dstHost"
    v-model:dst-port="forward.dstPort"
    @close="forward = emptyPortForwardModal()"
    @create="createForward" />
  <VmFilenamesModal
    v-model:active="memory.active"
    title="Create memory snapshot"
    kind="Memory snapshot"
    prompt="Create a memory snapshot for the"
    :description="MEMORY_SNAPSHOT_DOES"
    :vms="memory.vm"
    :valid="validFilenames(memory)"
    @close="memory = { active: false, vm: [] }"
    @update:filename="(index, name) => (memory.vm[index].filename = name)"
    @create="createMemorySnapshot" />
  <VmFilenamesModal
    v-model:active="image.active"
    title="Create a Disk Image"
    kind="Disk image"
    prompt="Create disk image of the"
    :vms="image.vm"
    :valid="validFilenames(image)"
    @close="image = { active: false, vm: [] }"
    @update:filename="(index, name) => (image.vm[index].filename = name)"
    @create="createImage" />
  <RedeployModal
    v-model:active="redeploy.active"
    :vms="redeploy.vm"
    :disks="disks"
    @close="redeploy = { active: false, vm: [] }"
    @edit="(index, field, value) => (redeploy.vm[index][field] = value)"
    @redeploy="sendRedeploy" />
  <VncInterfaceModal
    v-model:active="picker.active"
    :title="picker.title"
    :interfaces="picker.interfaces"
    :submit="picker.for === 'captureInterface' && !askFormat ? 'Start' : 'Next'"
    @pick="(index) => interfacePicked(picker.for, index)" />
  <VmLinkQualityModal
    v-if="quality.experiment"
    v-model:active="quality.active"
    :experiment="quality.experiment"
    :vm-name="quality.vm"
    :index="quality.index"
    :network="quality.network" />
  <CaptureFormatModal
    v-model:active="format.active"
    :title="format.title"
    @start="startCapture" />
  <OpticalDiscModal
    v-model:active="disc.active"
    v-model:disc="disc.disc"
    :vm-name="disc.vmName"
    :current="disc.current"
    :isos="disc.isos"
    :loading="disc.loading"
    :error="disc.error"
    @after-leave="disc = emptyOpticalDiscModal()"
    @insert="insertDisc"
    @eject="ejectDisc" />
</template>
