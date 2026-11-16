<!--
The frame of the start, stop and restart dialogs of an experiment: a form with
the look of a Buefy confirm dialog, the options in its slot, and Cancel and the
confirm button. It emits confirm or cancel.
-->
<template>
  <form class="modal-card" novalidate @submit.prevent="submit">
    <header class="modal-card-head">
      <h2 class="modal-card-title">{{ title }}</h2>
    </header>
    <section class="modal-card-body is-flex">
      <div class="media">
        <div class="media-left">
          <b-icon :icon="icon" :type="confirmType" size="is-large" />
        </div>
        <div class="media-content">
          <p class="dialog-message">{{ message }}</p>
          <p v-if="note" class="mt-3 has-text-grey-lighter">{{ note }}</p>
          <slot />
        </div>
      </div>
    </section>
    <footer class="modal-card-foot">
      <b-button @click="$emit('cancel')">Cancel</b-button>
      <b-button
        ref="confirm"
        :type="confirmType"
        native-type="submit"
        :disabled="!valid">
        {{ confirmText }}
      </b-button>
    </footer>
  </form>
</template>

<script>
  export default {
    name: 'ExperimentActionDialog',
    props: {
      title: { type: String, required: true },
      // plain text, as are the other props
      message: { type: String, required: true },
      // which of a selection the action leaves out, and why
      note: { type: String, default: '' },
      icon: { type: String, required: true },
      confirmText: { type: String, required: true },
      confirmType: { type: String, required: true },
      valid: { type: Boolean, default: true },
    },
    emits: ['confirm', 'cancel'],
    mounted() {
      // As a confirm dialog does, focus the confirm button, so Enter confirms.
      // The modal focuses itself once shown, so this waits for that.
      this.focusTimer = setTimeout(() => this.$refs.confirm?.$el?.focus());
    },
    beforeUnmount() {
      clearTimeout(this.focusTimer);
    },
    methods: {
      submit() {
        if (this.valid) this.$emit('confirm');
      },
    },
  };
</script>

<style scoped>
  .modal-card-body :deep(.options) {
    margin-top: 0.75rem;
  }

  .modal-card-body :deep(.options .label) {
    margin-bottom: 0.25rem;
  }

  .modal-card-body :deep(.options .b-checkbox.checkbox) {
    display: flex;
    margin: 0.25rem 0;
  }
</style>
