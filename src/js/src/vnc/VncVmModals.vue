<!-- The dialogs of the VM actions of the console top bar that ask for more:
     the CD-ROM picker, a new port forward and the name of a memory
     snapshot. They are the dialogs of the experiment page. The page opens
     one for the controller of a pane, whose pending action spins while the
     request runs. -->
<script setup>
  import { getCurrentInstance, ref } from 'vue';

  import {
    createVmForward,
    createVmMemorySnapshot,
    ejectVmDisc,
    insertVmDisc,
  } from '@/api/vms.js';
  import OpticalDiscModal from '@/components/experiment/OpticalDiscModal.vue';
  import PortForwardModal from '@/components/experiment/PortForwardModal.vue';
  import VmFilenamesModal from '@/components/experiment/VmFilenamesModal.vue';
  import { MEMORY_SNAPSHOT_DOES } from '@/components/experiment/vmActions.js';
  import {
    emptyOpticalDiscModal,
    emptyPortForwardModal,
    loadIsoChoices,
    memorySnapshotEntry,
    openOpticalDiscModal,
    validFilenames,
  } from '@/components/experiment/vmTools.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { escapeHTML } from '@/utils/escapeHTML.js';

  const buefy = getCurrentInstance()?.proxy?.$buefy;

  const disc = ref(emptyOpticalDiscModal());
  const forward = ref(emptyPortForwardModal());
  const memory = ref({ active: false, vm: [] });
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
    } catch (err) {
      useErrorNotification(err);
    } finally {
      controller.pending = null;
    }
  }

  /**
   * Opens the dialog of the action for the VM of the controller.
   *
   * @param {'cdrom'|'portForward'|'memorySnapshot'} action
   * @param {{ experiment: string, vm: string, row: object|null,
   *   pending: string|null }} controller
   */
  async function open(action, controller) {
    target = controller;
    if (action === 'portForward') {
      forward.value = { ...emptyPortForwardModal(), active: true };
    } else if (action === 'memorySnapshot') {
      memory.value = { active: true, vm: [memorySnapshotEntry(controller.vm)] };
    } else if (action === 'cdrom') {
      disc.value = openOpticalDiscModal({
        name: controller.vm,
        cdRom: controller.row?.cdRom,
      });
      const modal = disc.value;
      await loadIsoChoices(modal, () => disc.value === modal);
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
