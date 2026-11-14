<template>
  <b-modal
    :model-value="active"
    @update:model-value="(on) => $emit('update:active', on)"
    has-modal-card
    ref="modal">
    <form class="modal-card capture-format-card" @submit.prevent="start">
      <header class="modal-card-head">
        <h2 class="modal-card-title">{{ title }}</h2>
      </header>
      <section class="modal-card-body">
        <FieldLabel :id="`${uid}-format`" label="Format" :help="FORMAT_HELP" />
        <div
          class="capture-format-choices"
          role="radiogroup"
          :aria-labelledby="`${uid}-format`">
          <b-radio
            v-for="f in CAPTURE_FORMATS"
            :key="f"
            v-model="format"
            :name="`${uid}-format`"
            :native-value="f">
            {{ f }}
          </b-radio>
        </div>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button" type="button" @click="$refs.modal.cancel('x')">
          Cancel
        </button>
        <button class="button is-success capture-format-start" type="submit">
          Start
        </button>
      </footer>
    </form>
  </b-modal>
</template>

<script>
  import FieldLabel from '@/components/FieldLabel.vue';
  import { CAPTURE_FORMATS, FORMAT_HELP } from './experimentCaptures.js';

  let dialogs = 0;

  // The small dialog that asks for the file format of VM interface captures
  // before they start, pcapng picked when it opens. Start emits the format.
  export default {
    name: 'CaptureFormatModal',
    components: { FieldLabel },
    props: {
      active: { type: Boolean, default: false },
      title: { type: String, required: true },
    },
    emits: ['update:active', 'start'],
    setup() {
      return {
        CAPTURE_FORMATS,
        FORMAT_HELP,
        uid: `capture-format-${++dialogs}`,
      };
    },
    data() {
      return { format: CAPTURE_FORMATS[0] };
    },
    watch: {
      active(on) {
        if (on) this.format = CAPTURE_FORMATS[0];
      },
    },
    methods: {
      start() {
        this.$emit('start', this.format);
        this.$emit('update:active', false);
      },
    },
  };
</script>

<style scoped>
  .capture-format-card {
    width: 26rem;
  }

  .capture-format-choices {
    display: flex;
    gap: 1.5rem;
  }
</style>
