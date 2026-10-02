<!-- A VM's disk picker. It offers the disks in the list by label, those outside
the minimega files directory in a group of their own, and gives the chosen
disk's full path. A VM disk the list does not hold is still shown. A warning
beside the picker says when the VM's disk is outside the files directory, or
when it is missing from the list, which is only known once the list has
loaded (null until then, and for roles that may not list disks). -->
<template>
  <div class="disk-select" :class="{ 'is-expanded': expanded }">
    <b-select
      :model-value="modelValue"
      :aria-label="label"
      :title="modelValue"
      :expanded="expanded"
      @update:model-value="(path) => $emit('update:modelValue', path)">
      <option v-if="!modelValue" value="" disabled>No disk</option>
      <option
        v-else-if="!listed"
        :value="modelValue"
        :disabled="warning === 'unlisted'">
        {{ warning === 'unlisted' ? `${modelValue} (not listed)` : modelValue }}
      </option>
      <option
        v-for="option in options.inside"
        :key="option.value"
        :value="option.value"
        :disabled="option.disabled">
        {{ option.label }}
      </option>
      <optgroup v-if="options.outside.length" :label="OUTSIDE_GROUP">
        <option
          v-for="option in options.outside"
          :key="option.value"
          :value="option.value"
          :disabled="option.disabled">
          {{ option.label }}
        </option>
      </optgroup>
    </b-select>
    <DiskWarning v-if="warning" :reason="warning" :files-dir="filesDir" />
  </div>
</template>

<script>
  import DiskWarning from '@/components/DiskWarning.vue';
  import {
    OUTSIDE_GROUP,
    diskOptions,
    diskWarning,
    filesDir,
    findDisk,
  } from '@/utils/diskChain.js';

  export default {
    name: 'DiskSelect',
    components: { DiskWarning },
    props: {
      // the VM's disk, by full path
      modelValue: { type: String, default: '' },
      // the disk list, or null while it is not known
      disks: { type: Array, default: null },
      // the picker's accessible name
      label: { type: String, required: true },
      expanded: Boolean,
    },
    emits: ['update:modelValue'],
    setup() {
      return { OUTSIDE_GROUP };
    },
    computed: {
      listed() {
        return findDisk(this.disks, this.modelValue);
      },
      options() {
        return diskOptions(this.disks);
      },
      warning() {
        return diskWarning(this.disks, this.modelValue);
      },
      filesDir() {
        return filesDir(this.disks);
      },
    },
  };
</script>

<style scoped>
  .disk-select {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    max-width: 100%;
  }

  .disk-select.is-expanded {
    display: flex;
  }

  .disk-select.is-expanded > .control {
    flex: 1 1 auto;
    min-width: 0;
  }
</style>
