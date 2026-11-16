<template>
  <b-field position="is-center" grouped class="stopped-selection-toolbar">
    <p class="control stopped-selection-count" role="status">
      <template v-if="pending">
        {{ PENDING_TEXT[pending] }} {{ plural(selected.length, 'VM') }}…
      </template>
      <template v-else>{{ selected.length }} selected</template>
    </p>
    <template v-if="allowed">
      <b-field v-for="button in shownButtons" :key="button.action">
        <b-tooltip :label="button.tooltip" type="is-light">
          <b-button
            :aria-label="button.label"
            :class="['button', button.type, button.action]"
            :icon-left="button.icon"
            :loading="pending == button.action"
            :disabled="disabled || (!!pending && pending != button.action)"
            @click="pending || $emit('act', button.action)">
          </b-button>
        </b-tooltip>
      </b-field>
    </template>
    <hr style="width: 1px; height: 100%; margin: 0" />
  </b-field>
</template>

<script>
  import { plural } from '@/utils/plural.js';
  import { roleAllowed } from '@/utils/rbac.js';

  const BUTTONS = [
    {
      action: 'boot',
      tooltip: 'set to boot',
      label: 'Set selected VMs to boot',
      type: 'is-light',
      icon: 'bolt',
    },
    {
      action: 'dnb',
      tooltip: 'set to not boot',
      label: 'Set selected VMs to not boot',
      type: 'is-light',
      icon: 'bolt',
    },
    {
      action: 'persistent',
      tooltip: 'make the disks persistent',
      label: 'Make the disks of selected VMs persistent',
      type: 'is-light',
      icon: 'save',
    },
    {
      action: 'nonPersistent',
      tooltip: 'make the disks non-persistent',
      label: 'Make the disks of selected VMs non-persistent',
      type: 'is-light',
      icon: 'layer-group',
    },
    {
      action: 'reset',
      tooltip: 'reset settings to the topology values',
      label: 'Reset settings of selected VMs',
      type: 'is-light',
      icon: 'undo-alt',
    },
  ];

  // what the count says while an action waits on the server
  const PENDING_TEXT = {
    boot: 'Setting to boot',
    dnb: 'Setting to not boot',
    persistent: 'Making persistent',
    nonPersistent: 'Making non-persistent',
    reset: 'Resetting',
  };

  // The toolbar of setting changes for the VMs selected in a stopped
  // experiment's VM table, shown when the role may change every one of them.
  // It asks the page, by event, to change the selection; while a change
  // waits on the server, its button spins and the count says what it does.
  export default {
    name: 'StoppedSelectionToolbar',
    setup() {
      return { PENDING_TEXT, plural };
    },
    props: {
      // the names of the selected VMs
      selected: { type: Array, required: true },
      // the experiment's name, which RBAC checks VM permissions under
      experiment: { type: String, default: '' },
      // the action waiting on the server, or null
      pending: { type: String, default: null },
      // whether the selection has VMs that can reset their settings
      canReset: { type: Boolean, default: false },
      // greys out every button, as while the experiment starts
      disabled: { type: Boolean, default: false },
    },
    // (action): boot, dnb, persistent, nonPersistent or reset
    emits: ['act'],
    computed: {
      allowed() {
        return this.selected.every((vm) =>
          roleAllowed('vms', 'patch', `${this.experiment}/${vm}`),
        );
      },
      shownButtons() {
        return BUTTONS.filter((b) => b.action != 'reset' || this.canReset);
      },
    },
  };
</script>

<style scoped>
  .stopped-selection-count {
    align-self: center;
    white-space: nowrap;
  }

  /* A lighter $primary hue keeps 3:1 on the button grays. */
  .button.boot :deep(.icon) {
    color: #ffb38c;
  }

  .button.dnb :deep(.icon) {
    color: #ffffff;
  }
</style>
