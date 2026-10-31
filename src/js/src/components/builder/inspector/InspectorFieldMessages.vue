<!--
  An Inspector field's error, warnings and hidden description (see
  InspectorField.vue).
-->
<template>
  <p v-if="errorText" :id="ids.error" class="error">
    <builder-icon name="close" :size="12" />
    {{ errorText }}
  </p>
  <p
    v-if="warnings.length"
    :id="ids.warning"
    class="warning"
    data-testid="inspector-field-warning">
    <builder-icon name="warning" :size="12" />
    <span><strong>Warning:</strong> {{ warnings.join(' ') }}</span>
  </p>
  <p v-if="description" :id="ids.description" hidden>
    {{ description }}
  </p>
</template>

<script setup>
  import BuilderIcon from '../BuilderIcon.vue';

  defineProps({
    // Ids of the error, the warnings and the description.
    ids: { type: Object, required: true },
    errorText: { type: String, default: '' },
    warnings: { type: Array, default: () => [] },
    description: { type: String, default: '' },
  });
</script>
