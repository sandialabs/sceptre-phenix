<!-- One setting of a stopped experiment's VM, as a control that changes it or
as text: its host, CPUs, memory, disk, inject partition, persistence or boot.
While a change saves, the control spins and shows the value picked, and keeps
its focus; a mark tells a value changed from the topology's. -->
<template>
  <span :class="['vm-setting', `vm-setting-${field}`]">
    <fieldset
      class="vm-setting-control"
      :aria-busy="pending ? 'true' : undefined">
      <template v-if="!editable">
        <template v-if="field == 'host'">
          {{ vm.external ? 'EXTERNAL' : vm.host || 'none' }}
        </template>
        <template v-else-if="field == 'disk'">
          <span :title="vm.disk">{{ diskText }}</span>
          <DiskWarning
            v-if="diskWarnings(disks, vm.disk).length"
            :reasons="diskWarnings(disks, vm.disk)"
            :detail="chainProblem(findDisk(disks, vm.disk))"
            :files-dir="filesDir(disks)" />
        </template>
        <template v-else-if="field == 'ram'">
          {{ vm.ram ? settingText('ram', vm.ram) : 'unknown' }}
        </template>
        <template v-else-if="field == 'snapshot' || field == 'dnb'">
          {{ settingText(field, vm[field]) }}
        </template>
        <template v-else>{{ vm[field] || 'unknown' }}</template>
      </template>

      <b-tooltip
        v-else-if="field == 'host'"
        label="assign the VM to a specific host"
        type="is-dark">
        <b-field>
          <b-select
            :aria-label="`Host for VM ${vm.name}`"
            :model-value="shown ?? ''"
            expanded
            @update:model-value="pick">
            <option v-if="!shown" value="" disabled>No host</option>
            <option v-for="h in hostChoices" :key="h" :value="h">
              {{ h }}
            </option>
          </b-select>
          <p class="control">
            <b-button
              :aria-label="`Unassign host from VM ${vm.name}`"
              class="button input-button"
              icon-right="window-close"
              :disabled="!shown"
              :aria-disabled="pending ? 'true' : undefined"
              @click="change('')" />
          </p>
        </b-field>
      </b-tooltip>

      <DiskSelect
        v-else-if="field == 'disk'"
        :model-value="shown ?? ''"
        :disks="disks"
        :label="`Disk for VM ${vm.name}`"
        expanded
        @update:model-value="pick" />

      <PartitionSelect
        v-else-if="field == 'inject_partition'"
        :model-value="shown"
        :disk="vm.disk"
        :label="`Partition for VM ${vm.name}`"
        @update:model-value="pick" />

      <b-tooltip
        v-else-if="field == 'snapshot'"
        :label="persistTip"
        type="is-dark"
        position="is-left">
        <button
          type="button"
          role="switch"
          :class="[
            'button',
            'is-small',
            'vm-persist-toggle',
            { 'is-persistent': !shown },
          ]"
          :aria-checked="shown ? 'false' : 'true'"
          :aria-label="`Persistent disk for VM ${vm.name}`"
          :aria-disabled="pending ? 'true' : undefined"
          @click="change(!shown)">
          <b-icon :icon="shown ? 'layer-group' : 'save'" size="is-small" />
          <span aria-hidden="true">{{
            shown ? 'Non-persistent' : 'Persistent'
          }}</span>
        </button>
      </b-tooltip>

      <b-tooltip v-else-if="field == 'dnb'" :label="bootTip" type="is-dark">
        <button
          type="button"
          role="switch"
          :class="['plain-button', 'vm-boot-toggle', { 'has-text': withText }]"
          :aria-checked="shown ? 'false' : 'true'"
          :aria-label="`Boot VM ${vm.name}`"
          :aria-disabled="pending ? 'true' : undefined"
          @click="change(!shown)">
          <font-awesome-icon :class="shown ? 'dnb' : 'boot'" icon="bolt" />
          <span v-if="withText" aria-hidden="true">{{
            shown ? 'Do not boot' : 'Boot'
          }}</span>
        </button>
      </b-tooltip>

      <b-tooltip v-else :label="selectTip" type="is-dark">
        <b-select
          :aria-label="`${FIELD_NAMES[field]} for VM ${vm.name}`"
          :model-value="shown"
          expanded
          @update:model-value="pick">
          <option v-for="n in numberChoices" :key="n" :value="n">
            {{ field == 'ram' ? settingText('ram', n) : n }}
          </option>
        </b-select>
      </b-tooltip>
    </fieldset>
    <!-- there before a change, so a screen reader says the Saving put in it -->
    <span class="vm-setting-status" role="status"
      ><span v-if="pending" class="vm-setting-spinner"
        ><span class="is-sr-only">Saving</span></span
      ></span
    >
    <b-tooltip
      v-if="changed"
      :label="changedTip(field, vm.defaults[field], disks)"
      type="is-dark"
      multilined>
      <span
        class="vm-setting-changed"
        role="img"
        tabindex="0"
        :aria-label="changedTip(field, vm.defaults[field], disks)"></span>
    </b-tooltip>
  </span>
</template>

<script>
  import DiskWarning from '@/components/DiskWarning.vue';
  import DiskSelect from './DiskSelect.vue';
  import PartitionSelect from './PartitionSelect.vue';
  import {
    chainProblem,
    diskLabel,
    diskWarnings,
    filesDir,
    findDisk,
  } from '@/utils/diskChain.js';
  import {
    CPU_CHOICES,
    RAM_CHOICES,
    changedSettings,
    changedTip,
    choicesWith,
    settingText,
  } from './vmSettings.js';

  // the name of each numeric setting in its control's accessible name
  const FIELD_NAMES = {
    cpus: 'CPUs',
    ram: 'Memory',
  };

  const SELECT_TIPS = {
    cpus: "menu for assigning the VM's CPUs",
    ram: "menu for assigning the VM's memory",
  };

  const CHOICES = {
    cpus: CPU_CHOICES,
    ram: RAM_CHOICES,
  };

  export default {
    name: 'VmSettingControl',
    components: { DiskSelect, DiskWarning, PartitionSelect },
    setup() {
      return {
        FIELD_NAMES,
        chainProblem,
        changedTip,
        diskWarnings,
        filesDir,
        findDisk,
        settingText,
      };
    },
    props: {
      // host, cpus, ram, disk, inject_partition, snapshot or dnb
      field: { type: String, required: true },
      vm: { type: Object, required: true },
      // the hosts a VM can be assigned to, and the disk list (null while it
      // is not known)
      hosts: { type: Array, default: () => [] },
      disks: { type: Array, default: null },
      // whether the role may change it; else it shows as text
      editable: { type: Boolean, default: false },
      // whether a change waits on the server, and the value it sets
      pending: { type: Boolean, default: false },
      pendingValue: { type: null, default: undefined },
      // whether the boot toggle says its state in words
      withText: { type: Boolean, default: false },
    },
    // (value): the value picked
    emits: ['change'],
    data() {
      // the value a select was set to while a change saved, sent after it
      return { picked: undefined };
    },
    watch: {
      pending(now) {
        if (now || this.picked === undefined) return;
        const value = this.picked;
        this.picked = undefined;
        if (String(value) !== String(this.vm[this.field] ?? '')) {
          this.$emit('change', value);
        }
      },
    },
    computed: {
      shown() {
        return this.pending ? this.pendingValue : this.vm[this.field];
      },
      hostChoices() {
        const hosts = [...this.hosts];
        if (this.shown && !hosts.includes(this.shown))
          hosts.unshift(this.shown);
        return hosts;
      },
      numberChoices() {
        return choicesWith(CHOICES[this.field] ?? [], this.shown);
      },
      selectTip() {
        return SELECT_TIPS[this.field] ?? '';
      },
      diskText() {
        return (
          diskLabel(findDisk(this.disks, this.vm.disk)) ||
          this.vm.disk ||
          'unknown'
        );
      },
      persistTip() {
        return this.shown
          ? `${this.vm.name}'s disk will not persist`
          : `${this.vm.name}'s disk will persist`;
      },
      bootTip() {
        return this.shown
          ? `${this.vm.name} does not boot when the experiment starts`
          : `${this.vm.name} boots when the experiment starts`;
      },
      changed() {
        return this.field in changedSettings(this.vm);
      },
    },
    methods: {
      change(value) {
        if (this.pending) return;
        this.$emit('change', value);
      },
      // A select shows what it was set to, so a value set while a change
      // saves is sent once that change is saved.
      pick(value) {
        if (this.pending) this.picked = value;
        else this.$emit('change', value);
      },
    },
  };
</script>

<style scoped>
  .vm-setting {
    position: relative;
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    max-width: 100%;
  }

  .vm-setting-control {
    min-width: 0;
    flex: 1 1 auto;
    margin: 0;
    padding: 0;
    border: 0;
  }

  .vm-setting-control :deep(.field) {
    margin-bottom: 0;
  }

  /* an empty status takes no room in the row */
  .vm-setting-status:empty {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .vm-setting-status {
    display: inline-flex;
  }

  .vm-setting-spinner {
    flex: none;
    width: 1em;
    height: 1em;
    border: 2px solid #dbdbdb;
    border-right-color: transparent;
    border-top-color: transparent;
    border-radius: 50%;
    animation: vm-setting-spin 0.6s linear infinite;
  }

  @keyframes vm-setting-spin {
    to {
      transform: rotate(360deg);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .vm-setting-spinner {
      animation-duration: 2s;
    }
  }

  /* 3:1 or more on the table and card grays */
  .vm-setting-changed {
    display: inline-block;
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background-color: #62c0d7;
    cursor: help;
  }

  .vm-persist-toggle {
    gap: 0.35rem;
    min-width: 9rem;
    justify-content: flex-start;
  }

  .vm-persist-toggle.is-persistent {
    border-color: #48c78e;
  }

  .vm-boot-toggle.has-text {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    color: inherit;
  }

  /* A lighter $primary hue keeps 3:1 on the button and table grays. */
  .boot {
    color: #ffb38c;
  }

  .dnb {
    color: #ffffff;
  }
</style>
