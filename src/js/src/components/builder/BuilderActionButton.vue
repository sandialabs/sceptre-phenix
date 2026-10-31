<!-- An item action (builder/itemActions.js): its icon, label and color, on a
     BuilderBusyButton. With busyText, that label shows while busy. -->
<template>
  <builder-busy-button
    ref="button"
    class="builder-action-button"
    :class="[toneClass(tone || spec.tone), `builder-action-button--${action}`]"
    :icon="spec.icon"
    :busy="busy"
    :disabled="disabled"
    :locked="locked"
    :type="type"
    @click="$emit('click', $event)">
    <span class="builder-button__swap">
      <span :class="{ 'is-off': busy && Boolean(busyText) }"
        ><slot>{{ spec.label }}</slot></span
      >
      <span v-if="busyText" :class="{ 'is-off': !busy }">{{ busyText }}</span>
    </span>
  </builder-busy-button>
</template>

<script setup>
  import { computed, ref } from 'vue';

  import BuilderBusyButton from './BuilderBusyButton.vue';

  import { itemAction, toneClass } from '@/builder/itemActions.js';

  const props = defineProps({
    action: {
      type: String,
      required: true,
      validator: (value) => Boolean(itemAction(value)),
    },
    busy: { type: Boolean, default: false },
    // The label while busy, such as Opening. It holds the button's width.
    busyText: { type: String, default: '' },
    disabled: { type: Boolean, default: false },
    locked: { type: Boolean, default: false },
    // A tone in place of the action's, such as default for a dialog's Open.
    tone: { type: String, default: '' },
    type: { type: String, default: 'button' },
  });

  defineEmits(['click']);

  const spec = computed(() => itemAction(props.action));

  const button = ref(null);

  defineExpose({
    focus: (options) => button.value?.focus(options),
  });
</script>
