<template>
  <b-field position="is-center" grouped group-multiline>
    <b-field
      v-for="button in modifyOpen ? STATE_BAR : MAIN_BAR"
      :key="button.action">
      <b-tooltip
        :label="deniedReason(button.action) ?? button.tooltip"
        type="is-light"
        multilined>
        <b-button
          :aria-label="button.label"
          :aria-disabled="deniedReason(button.action) ? 'true' : undefined"
          :class="['button', button.type]"
          :icon-left="button.icon"
          @click="deniedReason(button.action) || click(button.action)">
        </b-button>
      </b-tooltip>
    </b-field>
    <hr style="width: 1px; height: 100%; margin: 0" />
  </b-field>
</template>

<script>
  import { roleAllowed } from '@/utils/rbac.js';
  import { VM_ACTIONS, notPermittedReason } from './vmActions.js';

  // the toolbar's buttons, and those that take the place of all but the last
  // while the modify state actions are open
  const MAIN_BAR = [
    {
      action: 'start',
      tooltip: 'start',
      label: 'Start selected VMs',
      type: 'is-success',
      icon: 'play',
    },
    {
      action: 'pause',
      tooltip: 'pause',
      label: 'Pause selected VMs',
      type: 'is-warning',
      icon: 'pause',
    },
    {
      action: 'memorySnapshot',
      tooltip: 'create memory snapshot',
      label: 'Create memory snapshots of selected VMs',
      type: 'is-light',
      icon: 'database',
    },
    {
      action: 'commit',
      tooltip: 'create backing image',
      label: 'Create backing images for selected VMs',
      type: 'is-light',
      icon: 'save',
    },
    {
      action: 'snapshot',
      tooltip: 'create VM snapshot',
      label: 'Create snapshots of selected VMs',
      type: 'is-light',
      icon: 'clone',
    },
    {
      action: 'modify',
      tooltip: 'modify state',
      label: 'Modify state of selected VMs',
      type: 'is-light',
      icon: 'edit',
    },
  ];
  const STATE_BAR = [
    {
      action: 'redeploy',
      tooltip: 'redeploy',
      label: 'Redeploy selected VMs',
      type: 'is-success',
      icon: 'history',
    },
    {
      action: 'resetDisk',
      tooltip: 'reset disk state',
      label: 'Reset disk state of selected VMs',
      type: 'is-success',
      icon: 'undo-alt',
    },
    {
      action: 'restart',
      tooltip: 'restart',
      label: 'Restart selected VMs',
      type: 'is-success',
      icon: 'sync-alt',
    },
    {
      action: 'shutdown',
      tooltip: 'shutdown',
      label: 'Shut down selected VMs',
      type: 'is-danger',
      icon: 'power-off',
    },
    {
      action: 'kill',
      tooltip: 'kill',
      label: 'Kill selected VMs',
      type: 'is-danger',
      icon: 'skull-crossbones',
    },
    {
      action: 'close',
      tooltip: 'close toolbar',
      label: 'Close state actions',
      type: 'is-light',
      icon: 'window-close',
    },
  ];

  // The toolbar of actions on the VMs selected in the VM table. An action
  // that the role may not take on every one of them is greyed out, with the
  // permission it needs in its tooltip. It asks the page, by event, to take
  // an action on the selection.
  export default {
    name: 'VmSelectionToolbar',
    setup() {
      return { MAIN_BAR, STATE_BAR };
    },
    props: {
      // the names of the selected VMs
      selected: { type: Array, required: true },
      // the experiment's name, which RBAC checks VM permissions under
      experiment: { type: String, default: '' },
      // whether the modify state actions are open
      modifyOpen: { type: Boolean, default: false },
    },
    emits: [
      // (action): a VM action (see vmActions.js) to take on the selection
      'act',
      'update:modifyOpen',
    ],

    methods: {
      // Why the role may not take the action on the selection: the first
      // permission it lacks on one of the VMs, or null when it may.
      deniedReason(action) {
        const rbac = VM_ACTIONS[action]?.rbac ?? [];
        for (const vm of this.selected) {
          const missing = rbac.find(
            ([resource, verb]) =>
              !roleAllowed(resource, verb, `${this.experiment}/${vm}`),
          );
          if (missing) return notPermittedReason(...missing);
        }
        return null;
      },

      click(action) {
        if (action == 'modify') {
          this.$emit('update:modifyOpen', true);
        } else if (action == 'close') {
          this.$emit('update:modifyOpen', false);
        } else {
          this.$emit('act', action);
        }
      },
    },
  };
</script>
