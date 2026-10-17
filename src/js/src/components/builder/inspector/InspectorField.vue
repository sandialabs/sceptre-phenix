<!--
  Label, help text, error and warnings around one Inspector input.

  The help text is the schema's description of the field. It is a tooltip on
  the label, shown on hover and keyboard focus (see fieldTooltip.js), and the
  input's accessible description, read once. The tooltip of a field of the
  phenix node spec starts with the field's path in the spec (see
  InspectorTooltip.vue). A label with one is underlined
  with dots. The error and the warnings show under the input. An error is
  in the danger color with a cross, and marks the input invalid. A warning
  is in the warning color with a triangle and "Warning:", and does not. A
  note (see useFieldNote) shows under them in muted text, and describes the
  input too.

  A field showing its default (see useFieldDefault) says "Default" beside its
  input, and where the value comes from to a screen reader. While a field
  with a default has another value, "Default" is a button that resets it
  (see useDefaultReset), and focus goes back to the input. A field the
  working copy changed has a bar at its side until the change is applied or
  cancelled, and says so in its description (see useFieldChanged).

  The control in the default slot renders the input itself, with the ids
  and ARIA attributes from useInspectorControl(). The label has the id
  `ids.label` when the control names one, for an input that the label does
  not name alone. The label has no space
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
    @focusin="onFocusIn"
    @focusout="tooltip?.hide()">
    <label
      :id="ids.label"
      ref="label"
      :for="ids.input"
      class="label"
      :class="{
        required: control.required,
        'label--described': Boolean(control.description),
      }"
      @mouseenter="tooltip?.show(label, $event)"
      @mouseleave="tooltip?.scheduleHide()"
      >{{ control.label || 'Value'
      }}<span v-if="control.required" class="asterisk" aria-hidden="true">
        *</span
      ></label
    >
    <div
      ref="wrapper"
      class="wrapper"
      :class="{
        'wrapper--default': Boolean(fallback),
        'wrapper--reset': Boolean(reset),
      }">
      <slot />
      <!-- Screen readers get what the default is, as the input's
           description. "Default" alone is for the eye. -->
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
      <button
        v-else-if="reset"
        type="button"
        class="control__default control__reset"
        :aria-label="reset.name"
        :aria-describedby="ids.reset"
        title="Select to use the default value."
        data-testid="inspector-field-reset-default"
        @click="onReset">
        <builder-icon name="undo" :size="12" />
        Default
      </button>
    </div>
    <inspector-field-messages
      :ids="ids"
      :error-text="errorText"
      :warnings="warnings"
      :description="control.description" />
    <p
      v-if="note"
      :id="ids.note"
      class="note"
      data-testid="inspector-field-note">
      {{ note }}
    </p>
    <p v-if="changed" :id="ids.changed" hidden>Changed, not applied yet.</p>
    <p v-if="reset" :id="ids.reset" hidden>{{ reset.description }}</p>
    <inspector-tooltip
      v-if="control.description"
      ref="tooltip"
      :text="control.description"
      :path="specFieldPath(control.path)" />
  </div>
</template>

<script setup>
  import { nextTick, ref } from 'vue';

  import { specFieldPath } from '@/builder/formOutline.js';

  import BuilderIcon from '../BuilderIcon.vue';
  import InspectorFieldMessages from './InspectorFieldMessages.vue';
  import InspectorTooltip from './InspectorTooltip.vue';

  const props = defineProps({
    control: { type: Object, required: true },
    ids: { type: Object, required: true },
    errorText: { type: String, default: '' },
    warnings: { type: Array, default: () => [] },
    fallback: { type: Object, default: undefined },
    changed: { type: Boolean, default: false },
    // The note under the input (see useFieldNote), as useInspectorControl
    // gives it, so the input's description names the note shown.
    note: { type: String, default: '' },
    // The name and description of the Default button (see useDefaultReset),
    // or undefined for none.
    reset: { type: Object, default: undefined },
  });

  const emit = defineEmits(['reset']);

  const label = ref();
  const tooltip = ref();
  const wrapper = ref();

  // The Default button has a tooltip of its own.
  function onFocusIn(event) {
    if (event.target.closest?.('.control__reset')) {
      tooltip.value?.hide();
    } else {
      tooltip.value?.onFocusIn(event, label.value);
    }
  }

  // The button goes as the field shows its default: focus goes to the input.
  async function onReset() {
    emit('reset');
    await nextTick();
    wrapper.value?.querySelector(`#${CSS.escape(props.ids.input)}`)?.focus();
  }
</script>
