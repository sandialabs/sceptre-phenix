<!-- A warning icon beside a disk image's name, saying why the image needs
care: its backing chain is broken, it is outside the minimega files directory,
or it is not in the disk list. A broken chain shows a different icon. Hovering
or focusing the icon shows why, and Escape hides it again; the tooltip is added
to the page body so a table or card never clips it. -->
<template>
  <span class="disk-warning">
    <b-tooltip
      ref="tooltip"
      :label="text"
      type="is-dark"
      size="is-large"
      content-class="disk-warning-content"
      multilined
      append-to-body
      :triggers="['hover', 'focus']">
      <button
        type="button"
        class="disk-warning-button"
        :aria-label="name"
        :aria-describedby="`${id}-text`"
        @keyup.esc="dismiss">
        <b-icon
          :icon="broken ? 'times-circle' : 'exclamation-triangle'"
          size="is-small"
          :class="broken ? 'broken-chain-icon' : 'has-text-warning'" />
      </button>
    </b-tooltip>
    <span :id="`${id}-text`" class="is-sr-only">{{ text }}</span>
  </span>
</template>

<script>
  import { useId } from 'vue';

  import { OUTSIDE_GROUP } from '@/utils/diskChain.js';

  // each reason's short name, the button's accessible name
  const NAMES = {
    broken: 'Broken backing chain',
    outside: OUTSIDE_GROUP,
    unlisted: 'Not in your disk list',
  };

  export default {
    name: 'DiskWarning',
    props: {
      // why, the most serious first: its chain is 'broken', it is 'outside'
      // the minimega files directory, or it is 'unlisted' (see diskWarnings)
      reasons: {
        type: Array,
        required: true,
        validator: (reasons) =>
          reasons.length > 0 &&
          reasons.every((r) => ['broken', 'outside', 'unlisted'].includes(r)),
      },
      // what breaks the chain, for 'broken' (see chainProblem)
      detail: { type: String, default: '' },
      // the minimega files directory, when known
      filesDir: { type: String, default: '' },
    },
    setup() {
      return { id: useId() };
    },
    computed: {
      broken() {
        return this.reasons.includes('broken');
      },
      name() {
        return this.reasons.map((reason) => NAMES[reason]).join(', ');
      },
      text() {
        return this.reasons.map((reason) => this.reasonText(reason)).join(' ');
      },
    },
    methods: {
      reasonText(reason) {
        if (reason === 'broken') {
          const why = this.detail ? `: ${this.detail}` : '';
          return (
            `This image's backing chain is broken${why}. Starting the ` +
            'experiment may fail.'
          );
        }
        if (reason === 'unlisted') {
          return (
            'This image is not in your disk list: it may be missing, ' +
            'unreadable, or hidden from your role.'
          );
        }
        const where = this.filesDir ? ` (${this.filesDir})` : '';
        return (
          `${OUTSIDE_GROUP}${where}. minimega does not copy this image to ` +
          'other cluster nodes. On a multi-node cluster, every node that ' +
          'may run a VM with this image needs the same file at this path.'
        );
      },
      // Escape hides the explanation, and goes no further while it shows: a
      // Buefy modal around the warning closes on the same key's keyup on the
      // document.
      dismiss(event) {
        const { tooltip } = this.$refs;
        if (!tooltip.isActive) return;
        event.stopPropagation();
        tooltip.close();
      },
    },
  };
</script>

<style scoped>
  .disk-warning {
    display: inline-flex;
    align-items: center;
    vertical-align: middle;
  }

  .disk-warning-button {
    display: inline-flex;
    align-items: center;
    padding: 0;
    border: 0;
    background: none;
    cursor: help;
  }

  .disk-warning-button .icon {
    width: 1rem;
    height: 1rem;
  }
</style>

<style>
  /* the tooltip is added to the page body, out of reach of scoped styles */
  .b-tooltip.is-multiline .tooltip-content.disk-warning-content {
    text-align: left;
  }
</style>
