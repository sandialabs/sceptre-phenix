<template>
  <b-tooltip :label="tooltip" type="is-dark" :position="position">
    <b-button
      class="apps-box-trigger"
      type="is-success"
      size="is-small"
      icon-left="play"
      aria-label="Trigger apps"
      :disabled="!!blocked"
      @click="$emit('trigger')"
      >Trigger</b-button
    >
  </b-tooltip>
</template>

<script>
  import { roleAllowed } from '@/utils/rbac.js';
  import { triggerBlocked } from './experimentHeader.js';

  // The Trigger button that opens the dialog to trigger a running
  // experiment's apps, greyed out with the reason when the user cannot.
  export default {
    name: 'AppsTriggerButton',
    props: {
      experiment: { type: String, default: '' },
      apps: { type: Array, default: () => [] },
      // where its tooltip opens
      position: { type: String, default: 'is-left' },
    },
    // (): open the trigger dialog
    emits: ['trigger'],
    computed: {
      blocked() {
        return triggerBlocked(
          roleAllowed('experiments/trigger', 'create', this.experiment),
          this.apps,
        );
      },
      tooltip() {
        return this.blocked ?? 'pick apps to trigger';
      },
    },
  };
</script>
