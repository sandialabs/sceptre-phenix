<!-- A VM's inject partition picker: the partitions of the VM's disk, each with
its file system and start, read from the disk's details once the picker is
first pointed at or focused. While they load, or when phēnix cannot read
them, it offers partitions 1 to 4 and says why. -->
<template>
  <b-tooltip :label="tip" type="is-dark" multilined>
    <div class="partition-select" @focusin="want" @pointerenter="want">
      <b-select
        :aria-label="label"
        :aria-describedby="note ? noteId : undefined"
        :model-value="modelValue"
        expanded
        @update:model-value="(n) => $emit('update:modelValue', n)">
        <option v-for="c in choices" :key="c.value" :value="c.value">
          {{ c.label }}
        </option>
      </b-select>
      <span v-if="note" :id="noteId" class="is-sr-only">{{ note }}</span>
    </div>
  </b-tooltip>
</template>

<script>
  import { useId } from 'vue';

  import { baseName } from '@/utils/diskChain.js';
  import {
    cachedDiskDetails,
    partitionChoices,
    partitionsUnread,
    readDiskDetails,
  } from '@/utils/diskDetails.js';
  import { errorLine } from '@/utils/operations.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import { PARTITION_CHOICES, choicesWith } from './vmSettings.js';

  const TIP = 'menu for assigning inject partition';
  const FALLBACK = 'The menu shows partitions 1 to 4.';

  export default {
    name: 'PartitionSelect',
    props: {
      // the VM's inject partition
      modelValue: { type: [Number, String], default: null },
      // the full path of the VM's disk
      disk: { type: String, default: '' },
      // the picker's accessible name
      label: { type: String, required: true },
    },
    emits: ['update:modelValue'],
    setup() {
      return { noteId: useId() };
    },
    data() {
      return {
        // whether the picker was used, so it reads the disk's partitions
        wanted: false,
        // the last details read, and why a read failed, each with its disk
        read: null,
        failed: null,
      };
    },
    computed: {
      details() {
        return this.read?.disk === this.disk ? this.read.details : null;
      },
      failure() {
        return this.failed?.disk === this.disk ? this.failed.text : '';
      },
      canRead() {
        return roleAllowed('disks', 'list', baseName(this.disk));
      },
      // the disk's partitions, or null when they are not known
      listed() {
        return partitionChoices(this.details, this.modelValue);
      },
      choices() {
        return (
          this.listed ??
          choicesWith(PARTITION_CHOICES, this.modelValue).map((n) => ({
            value: n,
            label: String(n),
          }))
        );
      },
      // why the picker shows partitions 1 to 4, or '' when it does not
      note() {
        const why = this.listed ? '' : this.unread;
        return why && `${why} ${FALLBACK}`;
      },
      // why the disk's partitions are not known, or '' before it is used
      unread() {
        if (this.details) return partitionsUnread(this.details);
        if (!this.wanted) return '';
        if (!this.disk) return 'The VM has no disk.';
        if (!this.canRead) {
          return 'Your role cannot read the partitions of the disk.';
        }
        if (this.failure) {
          return `phēnix cannot read the partitions of the disk: ${this.failure}.`;
        }
        return 'Reading the partitions of the disk.';
      },
      tip() {
        return this.note || TIP;
      },
    },
    watch: {
      disk: {
        immediate: true,
        handler(disk) {
          const cached = cachedDiskDetails(disk);
          if (cached) this.read = { disk, details: cached };
          if (this.wanted) this.load();
        },
      },
    },
    methods: {
      want() {
        if (this.wanted) return;
        this.wanted = true;
        this.load();
      },
      // Reads the disk's details; an answer for a disk the VM no longer has
      // is dropped.
      async load() {
        const disk = this.disk;
        if (!disk || !this.canRead) return;
        try {
          const details = await readDiskDetails(disk);
          if (disk === this.disk) this.read = { disk, details };
        } catch (err) {
          if (disk === this.disk)
            this.failed = { disk, text: errorLine(err).replace(/\.$/, '') };
        }
      },
    },
  };
</script>
