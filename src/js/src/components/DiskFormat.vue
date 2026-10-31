<!-- A disk image's type, such as qcow2 v3, with an icon when the server found
an issue with its format: a warning icon for an old format, or a format whose
backing chain phēnix does not check, and an error icon for an image that qemu
refuses. Hovering or focusing the icon shows the problem and the fix, and
Escape hides it again; the tooltip is added to the page body so a table or
card never clips it. -->
<template>
  <span class="disk-format">
    <span class="disk-type">{{ type }}</span>
    <template v-if="level">
      <b-tooltip
        ref="tooltip"
        :label="text"
        type="is-dark"
        size="is-large"
        content-class="disk-format-content"
        multilined
        append-to-body
        :triggers="['hover', 'focus']">
        <button
          type="button"
          class="disk-format-button"
          :aria-label="name"
          :aria-describedby="`${id}-text`"
          @keyup.esc="dismiss">
          <b-icon
            :icon="level === 'error' ? 'times-circle' : 'exclamation-triangle'"
            size="is-small"
            :class="
              level === 'error' ? 'broken-chain-icon' : 'has-text-warning'
            " />
        </button>
      </b-tooltip>
      <span :id="`${id}-text`" class="is-sr-only">{{ text }}</span>
    </template>
  </span>
</template>

<script>
  import { useId } from 'vue';

  import {
    diskType,
    formatIssueLevel,
    formatIssueName,
    formatIssueText,
  } from '@/utils/diskFormat.js';

  export default {
    name: 'DiskFormat',
    props: {
      // a disk as the disk list gives it
      disk: { type: Object, required: true },
    },
    setup() {
      return { id: useId() };
    },
    computed: {
      type() {
        return diskType(this.disk);
      },
      level() {
        return formatIssueLevel(this.disk);
      },
      name() {
        return formatIssueName(this.disk);
      },
      text() {
        return formatIssueText(this.disk);
      },
    },
    methods: {
      // Escape hides the explanation, and goes no further while it shows: a
      // Buefy modal around the icon closes on the same key's keyup on the
      // document.
      dismiss(event) {
        const { tooltip } = this.$refs;
        if (!tooltip?.isActive) return;
        event.stopPropagation();
        tooltip.close();
      },
    },
  };
</script>

<style scoped>
  .disk-format {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    white-space: nowrap;
  }

  .disk-format-button {
    display: inline-flex;
    align-items: center;
    padding: 0;
    border: 0;
    background: none;
    cursor: help;
  }

  .disk-format-button .icon {
    width: 1rem;
    height: 1rem;
  }
</style>

<style>
  /* the tooltip is added to the page body, out of reach of scoped styles */
  .b-tooltip.is-multiline .tooltip-content.disk-format-content {
    text-align: left;
    overflow-wrap: anywhere;
  }
</style>
