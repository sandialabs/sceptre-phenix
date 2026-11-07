<template>
  <b-field position="is-center" grouped>
    <template v-if="!modifyOpen">
      <template v-for="button in MAIN_BAR" :key="button.action">
        <b-field v-if="button.action == 'modify' || allows(button.action)">
          <b-tooltip :label="button.tooltip" type="is-light">
            <b-button
              :aria-label="button.label"
              :class="['button', button.type]"
              :icon-left="button.icon"
              @click="click(button.action)">
            </b-button>
          </b-tooltip>
        </b-field>
      </template>
    </template>
    <template v-else>
      <b-field v-for="button in STATE_BAR" :key="button.action">
        <b-tooltip
          v-if="button.action == 'close' || allows(button.action)"
          :label="button.tooltip"
          type="is-light">
          <b-button
            :aria-label="button.label"
            :class="['button', button.type]"
            :icon-left="button.icon"
            @click="click(button.action)">
          </b-button>
        </b-tooltip>
      </b-field>
    </template>
    <hr style="width: 1px; height: 100%; margin: 0" />
  </b-field>
</template>

<script>
  import { roleAllowed } from '@/utils/rbac.js';
  import { actionPermitted } from './vmActions.js';

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
      icon: 'camera',
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

  // The toolbar of actions on the VMs selected in the VM table, each shown
  // when the role may take it on every one of them. It asks the page, by
  // event, to take an action on the selection.
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
      // whether the role may take the action on every selected VM
      allows(action) {
        return this.selected.every((vm) =>
          actionPermitted(action, (resource, verb) =>
            roleAllowed(resource, verb, `${this.experiment}/${vm}`),
          ),
        );
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
