<!--
  Label, help text, error and warnings around one Inspector input.

  The help text is the schema's description of the field. It is a tooltip on
  the label, shown on hover and keyboard focus (see fieldTooltip.js), and the
  input's accessible description, read once. A label with one is underlined
  with dots. The error and the warnings are shown under the input: an error
  in the danger color with a cross, which marks the input invalid; a warning
  in the warning color with a triangle and "Warning:", which does not.

  A field showing its default (see useFieldDefault) says "Default" beside its
  input, and where the value comes from to a screen reader. A field the
  working copy changed has a bar at its side until the change is applied or
  cancelled, and says so in its description (see useFieldChanged).

  The input itself is rendered by the control in the default slot, with the
  ids and ARIA attributes from useInspectorControl(). The label has no space
  before its asterisk, which would otherwise end the input's accessible name.
-->
<template>
  <div
    :id="control.id"
    class="control"
    :class="{
      'control--invalid': Boolean(errorText),
      'control--changed': changed,
    }"
    :data-path="control.path"
    @focusin="tooltip?.onFocusIn($event, label)"
    @focusout="tooltip?.hide()">
    <label
      ref="label"
      :for="ids.input"
      class="label"
      :class="{
        required: control.required,
        'label--described': Boolean(control.description),
      }"
      @mouseenter="tooltip?.show(label)"
      @mouseleave="tooltip?.scheduleHide()"
      >{{ control.label || 'Value'
      }}<span v-if="control.required" class="asterisk" aria-hidden="true">
        *</span
      ></label
    >
    <div class="wrapper" :class="{ 'wrapper--default': Boolean(fallback) }">
      <slot />
      <!-- Screen readers get what the default is, as the input's
           description; "Default" alone is for the eye. -->
      <span
        v-if="fallback"
        :id="ids.fallback"
        class="control__default"
        data-testid="inspector-field-default">
        <span aria-hidden="true">Default</span>
        <span class="builder-visually-hidden"
          >Default: {{ fallback.note }}.</span
        >
      </span>
    </div>
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
    <p v-if="control.description" :id="ids.description" hidden>
      {{ control.description }}
    </p>
    <p v-if="changed" :id="ids.changed" hidden>Changed, not applied yet.</p>
    <inspector-tooltip
      v-if="control.description"
      ref="tooltip"
      :text="control.description" />
  </div>
</template>

<script setup>
  import { ref } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import InspectorTooltip from './InspectorTooltip.vue';

  defineProps({
    control: { type: Object, required: true },
    ids: { type: Object, required: true },
    errorText: { type: String, default: '' },
    warnings: { type: Array, default: () => [] },
    fallback: { type: Object, default: undefined },
    changed: { type: Boolean, default: false },
  });

  const label = ref();
  const tooltip = ref();
</script>
