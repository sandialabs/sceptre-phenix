<!-- A form field's label with a help icon right after it. Hovering or focusing
the icon shows the help; the tooltip is added to the page body so a card's
scrolling body never clips it. Label the field's control with
aria-labelledby="<id>". -->
<template>
  <div class="field-label-help">
    <label :id="id" class="label">{{ label }}</label>
    <b-tooltip
      class="field-help"
      :label="help"
      type="is-dark"
      position="is-right"
      size="is-large"
      content-class="field-help-content"
      multilined
      append-to-body
      :triggers="['hover', 'focus']">
      <button
        type="button"
        class="field-help-button"
        :aria-label="`About ${label}`"
        :aria-describedby="`${id}-help`">
        <b-icon icon="question-circle" size="is-small" />
      </button>
    </b-tooltip>
    <span :id="`${id}-help`" class="is-sr-only">{{ help }}</span>
  </div>
</template>

<script>
  export default {
    name: 'FieldLabel',
    props: {
      id: { type: String, required: true },
      label: { type: String, required: true },
      help: { type: String, required: true },
    },
  };
</script>

<style scoped>
  .field-label-help {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    margin-bottom: 0.5em;
  }

  .field-label-help .label {
    margin-bottom: 0;
  }

  .field-help-button {
    display: inline-flex;
    align-items: center;
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    cursor: help;
    opacity: 0.85;
  }

  .field-help-button:hover,
  .field-help-button:focus-visible {
    opacity: 1;
  }

  .field-help-button .icon {
    width: 0.9rem;
    height: 0.9rem;
  }
</style>

<style>
  /* the tooltip is added to the page body, out of reach of scoped styles */
  .b-tooltip.is-multiline .tooltip-content.field-help-content {
    text-align: left;
  }
</style>
