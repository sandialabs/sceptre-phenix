<template>
  <div v-if="!vm.external" class="row-actions">
    <b-tooltip
      v-for="action in actions"
      :key="action.name"
      :label="action.reason || action.description || action.label"
      type="is-dark"
      multilined>
      <button
        :class="[
          'button',
          action.type || 'is-light',
          'is-small',
          `vm-row-${action.name}`,
          { 'is-loading': action.pending },
        ]"
        :aria-label="action.label"
        :aria-busy="action.pending"
        :disabled="action.disabled || action.pending"
        @click="$emit('action', action.name)">
        <b-icon :icon="action.icon" />
      </button>
    </b-tooltip>
  </div>
</template>

<script>
  import { roleAllowed } from '@/utils/rbac.js';
  import { rowActionNames, vmActionStates } from './vmActions.js';

  // The VM table's Actions column: compact icon buttons, disabled with the
  // reason in their tooltip when the role or the VM's state rules them out,
  // as in the Disks table. They wrap onto more rows rather than widen the
  // column.
  export default {
    name: 'VmRowActions',
    props: {
      vm: { type: Object, required: true },
      // experiment/vm, the name RBAC checks this VM's permissions against
      fullName: { type: String, required: true },
      // the actions still running for this VM, shown as spinners
      pending: { type: Array, default: () => [] },
    },
    emits: ['action'],
    computed: {
      actions() {
        return vmActionStates(rowActionNames(this.vm), this.vm, {
          can: (resource, verb) => roleAllowed(resource, verb, this.fullName),
        }).map((action) => ({
          ...action,
          pending: this.pending.includes(action.name),
        }));
      },
    },
  };
</script>

<style scoped>
  /* three buttons to a row, centered, the rest wrapping below */
  .row-actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 4px;
    max-width: calc(3 * 2.25rem + 2 * 4px);
    margin: 0 auto;
  }

  .row-actions :deep(.b-tooltip) {
    display: block;
  }

  .row-actions .button {
    width: 2.25rem;
  }
</style>
