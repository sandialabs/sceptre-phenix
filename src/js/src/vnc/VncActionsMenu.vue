<!-- The Actions menu of the console top bar: the VM actions that are not
     icon buttons, one to a row with its icon, in groups with a line between
     them. The last group, which kills or replaces the VM, has red text. An
     action that the role or the VM state rules out stays in the menu,
     greyed out, with the reason in its tooltip. -->
<script setup>
  import { onMounted, ref } from 'vue';

  defineProps({
    // the groups of action states (see vmActionState), in order
    groups: { type: Array, required: true },
    // the index of the group with red text, or -1
    dangerGroup: { type: Number, default: -1 },
    // an action of the menu runs: the trigger spins
    pending: { type: Boolean, default: false },
  });

  // open: the menu opened, so the panels of the bar close
  const emit = defineEmits(['action', 'open']);

  const dropdown = ref(null);
  const open = ref(false);

  // Buefy puts the menu role on an inner element, which gets its name here
  onMounted(() => {
    dropdown.value?.$el
      .querySelector('.dropdown-content')
      ?.setAttribute('aria-label', 'VM actions');
  });

  /** Closes the menu, as when the top bar hides. */
  function close() {
    if (open.value) dropdown.value?.toggle();
  }

  function onActiveChange(active) {
    open.value = active;
    if (active) emit('open');
  }

  function pick(action) {
    if (!action.disabled) emit('action', action.name);
  }

  defineExpose({ close });
</script>

<template>
  <b-tooltip
    label="More actions on the VM"
    type="is-dark"
    position="is-bottom"
    :active="!open">
    <b-dropdown
      ref="dropdown"
      class="vnc-actions-menu"
      aria-role="menu"
      position="is-bottom-left"
      @active-change="onActiveChange">
      <template #trigger>
        <b-button
          size="is-small"
          class="vnc-actions-trigger"
          icon-left="bars"
          :loading="pending"
          :aria-busy="pending ? 'true' : undefined">
          Actions
        </b-button>
      </template>
      <template v-for="(group, index) in groups" :key="index">
        <hr v-if="index > 0" class="dropdown-divider" />
        <b-dropdown-item
          v-for="action in group"
          :key="action.name"
          aria-role="menuitem"
          :class="[
            'vnc-actions-item',
            `vnc-actions-item-${action.name}`,
            { 'vnc-actions-item--danger': index === dangerGroup },
          ]"
          :disabled="action.disabled"
          :aria-disabled="action.disabled ? 'true' : undefined"
          @click="pick(action)">
          <b-tooltip
            :label="action.reason || action.description"
            type="is-dark"
            position="is-left"
            multilined
            class="vnc-actions-tip">
            <b-icon :icon="action.icon" size="is-small" />
            <span class="vnc-actions-label">{{ action.label }}</span>
          </b-tooltip>
        </b-dropdown-item>
      </template>
    </b-dropdown>
  </b-tooltip>
</template>
