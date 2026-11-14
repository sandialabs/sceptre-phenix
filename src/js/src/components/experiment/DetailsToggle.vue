<!-- The experiment header's Details button, which shows and hides its facts
     and Apps box. -->
<template>
  <button
    type="button"
    class="button is-light is-small experiment-header-toggle"
    :aria-expanded="String(expanded)"
    :aria-controls="controls">
    <b-icon
      :icon="expanded ? 'chevron-up' : 'chevron-down'"
      size="is-small"
      aria-hidden="true" />
    <span>Details</span>
  </button>
</template>

<script>
  export default {
    name: 'DetailsToggle',
    props: {
      expanded: { type: Boolean, default: false },
      // the ids of the parts it shows and hides
      controls: { type: String, default: '' },
    },
  };
</script>
