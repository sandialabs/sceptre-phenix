<template>
  <b-tooltip :label="tooltip" type="is-light" :delay="500" :active="!open">
    <b-dropdown
      :class="['vm-pick-filter', `vm-${kind}-filter`]"
      aria-role="dialog"
      position="is-bottom-left"
      :disabled="options.length === 0"
      @active-change="(active) => (open = active)">
      <template #trigger>
        <b-button
          :class="[
            'vm-pick-filter-trigger',
            `vm-${kind}-filter-trigger`,
            { 'is-filtering': picked.length },
          ]"
          type="is-light"
          :icon-left="icon"
          :aria-label="triggerLabel"
          :disabled="options.length === 0"
          >{{ title
          }}<span v-if="picked.length">&nbsp;({{ picked.length }})</span>
        </b-button>
      </template>
      <b-dropdown-item custom :focusable="false" class="vm-pick-filter-note">
        {{ note }}
      </b-dropdown-item>
      <div
        :class="['vm-pick-filter-list', `vm-${kind}-filter-list`]"
        role="group"
        :aria-label="`${title} to filter by`">
        <b-dropdown-item
          v-for="option in options"
          :key="idOf(option)"
          custom
          :focusable="false">
          <b-checkbox
            :model-value="isPicked(option)"
            :disabled="!canPick(option)"
            size="is-small"
            type="is-primary"
            @update:model-value="(on) => toggle(option, on)">
            {{ textOf(option) }}
          </b-checkbox>
        </b-dropdown-item>
      </div>
      <b-dropdown-item
        v-if="full"
        custom
        :focusable="false"
        class="vm-pick-filter-note">
        Uncheck {{ article }} {{ kind }} to check another
      </b-dropdown-item>
      <b-dropdown-item custom :focusable="false">
        <b-button
          size="is-small"
          icon-left="eraser"
          expanded
          :disabled="picked.length === 0"
          @click="$emit('update:picked', [])"
          >Clear {{ title.toLowerCase() }}</b-button
        >
      </b-dropdown-item>
    </b-dropdown>
  </b-tooltip>
</template>

<script>
  // Narrows the VM table to the VMs matching any of the options checked, such
  // as labels or hosts; VmLabelFilter and VmHostFilter word it for theirs.
  export default {
    name: 'VmPickFilter',
    props: {
      // what it filters by, in the singular ("label"), and as its button says
      kind: { type: String, required: true },
      title: { type: String, required: true },
      icon: { type: String, required: true },
      // the dropdown's first line, the button's tooltip, and why it is greyed
      // out with no options
      note: { type: String, required: true },
      hint: { type: String, required: true },
      emptyReason: { type: String, required: true },
      // the options to offer, and those checked
      options: { type: Array, default: () => [] },
      picked: { type: Array, default: () => [] },
      // an option's unique key and its text
      idOf: { type: Function, default: (option) => option },
      textOf: { type: Function, default: (option) => option },
      // whether the server can take a filter by these options
      fits: { type: Function, default: () => true },
    },
    emits: ['update:picked'],
    data() {
      // while the list is open, the tooltip that would cover it is hidden
      return { open: false };
    },
    computed: {
      article() {
        return /^[aeiou]/i.test(this.kind) ? 'an' : 'a';
      },
      tooltip() {
        if (this.options.length === 0) {
          return `Cannot filter by ${this.kind}: ${this.emptyReason}`;
        }
        return this.hint;
      },
      // some option is left unchecked because the filter is as long as it can be
      full() {
        return this.options.some((option) => !this.canPick(option));
      },
      triggerLabel() {
        const label = `Filter VMs by ${this.kind}`;
        return this.picked.length
          ? `${label}, ${this.picked.length} checked`
          : label;
      },
    },
    methods: {
      isPicked(option) {
        const id = this.idOf(option);
        return this.picked.some((p) => this.idOf(p) === id);
      },
      canPick(option) {
        return this.isPicked(option) || this.fits([...this.picked, option]);
      },
      toggle(option, on) {
        const id = this.idOf(option);
        const rest = this.picked.filter((p) => this.idOf(p) !== id);
        this.$emit('update:picked', on ? [...rest, option] : rest);
      },
    },
  };
</script>

<style scoped>
  /* room between icon and title, as a full-width glyph (server) fills its box */
  .vm-pick-filter-trigger :deep(.icon:first-child:not(:last-child)) {
    margin-inline-end: 0.5em;
  }

  /* a filter in use: a link-blue edge beside the count it shows */
  .vm-pick-filter-trigger.is-filtering {
    border-color: #62c0d7;
    box-shadow: inset 0 0 0 1px #62c0d7;
  }

  .vm-pick-filter-note {
    font-size: 0.75rem;
    font-style: italic;
    white-space: nowrap;
  }

  /* rules above and below show where a long list scrolls */
  .vm-pick-filter-list {
    max-height: 18rem;
    overflow-y: auto;
    overscroll-behavior: contain;
    border-block: 1px solid #686868;
  }

  .vm-pick-filter-list .dropdown-item {
    padding-block: 0.25rem;
  }

  /* an option left unchecked for length: its box faded, its name readable */
  .vm-pick-filter-list :deep(.checkbox[disabled]) {
    opacity: 1;
    color: #c8c8c8;
  }

  .vm-pick-filter-list :deep(.checkbox[disabled] .check) {
    opacity: 0.5;
  }
</style>
