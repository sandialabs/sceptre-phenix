<template>
  <b-tooltip :label="tooltip" type="is-dark" :delay="500" position="is-left">
    <b-dropdown
      class="vm-label-filter"
      aria-role="dialog"
      position="is-bottom-left"
      :disabled="options.length === 0">
      <template #trigger>
        <b-button
          :class="[
            'vm-label-filter-trigger',
            { 'is-filtering': picked.length },
          ]"
          type="is-light"
          size="is-small"
          icon-left="tag"
          :aria-label="triggerLabel"
          :disabled="options.length === 0"
          >Labels<span v-if="picked.length">&nbsp;({{ picked.length }})</span>
        </b-button>
      </template>
      <b-dropdown-item custom class="vm-label-filter-note">
        VMs with any of the checked labels
      </b-dropdown-item>
      <div
        class="vm-label-filter-list"
        role="group"
        aria-label="Labels to filter by">
        <b-dropdown-item v-for="label in options" :key="labelId(label)" custom>
          <b-checkbox
            :model-value="isPicked(label)"
            :disabled="!canPick(label)"
            size="is-small"
            type="is-primary"
            @update:model-value="(on) => toggle(label, on)">
            {{ labelText(label) }}
          </b-checkbox>
        </b-dropdown-item>
      </div>
      <b-dropdown-item v-if="full" custom class="vm-label-filter-note">
        Uncheck a label to check another
      </b-dropdown-item>
      <b-dropdown-item custom>
        <b-button
          size="is-small"
          icon-left="eraser"
          expanded
          :disabled="picked.length === 0"
          @click="$emit('update:picked', [])"
          >Clear labels</b-button
        >
      </b-dropdown-item>
    </b-dropdown>
  </b-tooltip>
</template>

<script>
  import { labelId, labelText } from './experimentHeader.js';

  // Narrows the VM table to the VMs carrying any of the labels checked, from
  // the labels on the experiment's VMs.
  export default {
    name: 'VmLabelFilter',
    setup() {
      return { labelId, labelText };
    },
    props: {
      // the labels to offer, and those checked, as { key, value }
      options: { type: Array, default: () => [] },
      picked: { type: Array, default: () => [] },
      // whether the server can take a filter by these labels
      fits: { type: Function, default: () => true },
    },
    emits: ['update:picked'],
    computed: {
      tooltip() {
        if (this.options.length === 0) {
          return "Can't filter by label: no VM has a label";
        }
        return 'show only the VMs with any of these labels';
      },
      // some label is left unchecked because the filter is as long as it can be
      full() {
        return this.options.some((label) => !this.canPick(label));
      },
      triggerLabel() {
        return this.picked.length
          ? `Filter VMs by label, ${this.picked.length} checked`
          : 'Filter VMs by label';
      },
    },
    methods: {
      isPicked(label) {
        const id = labelId(label);
        return this.picked.some((p) => labelId(p) === id);
      },
      canPick(label) {
        return this.isPicked(label) || this.fits([...this.picked, label]);
      },
      toggle(label, on) {
        const id = labelId(label);
        const rest = this.picked.filter((p) => labelId(p) !== id);
        this.$emit('update:picked', on ? [...rest, label] : rest);
      },
    },
  };
</script>

<style scoped>
  /* as tall as the Paginate switch beside it, so the toolbar keeps its height */
  .vm-label-filter-trigger {
    height: 1.6em;
    padding-top: 0;
    padding-bottom: 0;
  }

  /* a filter in use: a link-blue edge beside the count it shows */
  .vm-label-filter-trigger.is-filtering {
    border-color: #62c0d7;
    box-shadow: inset 0 0 0 1px #62c0d7;
  }

  .vm-label-filter-note {
    font-size: 0.75rem;
    font-style: italic;
    white-space: nowrap;
  }

  /* rules above and below show where a long list scrolls */
  .vm-label-filter-list {
    max-height: 18rem;
    overflow-y: auto;
    overscroll-behavior: contain;
    border-block: 1px solid #686868;
  }

  .vm-label-filter-list .dropdown-item {
    padding-block: 0.25rem;
  }

  /* a label left unchecked for length: its box faded, its name readable */
  .vm-label-filter-list :deep(.checkbox[disabled]) {
    opacity: 1;
    color: #c8c8c8;
  }

  .vm-label-filter-list :deep(.checkbox[disabled] .check) {
    opacity: 0.5;
  }
</style>
