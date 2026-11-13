<!-- The dialog of the console page that asks which interface of the VM an
     action is for, such as a packet capture or the link quality. An
     interface that the action cannot use is greyed out with the reason. The
     first interface that it can use is picked when the dialog opens. -->
<script setup>
  import { ref, useId, watch } from 'vue';

  const props = defineProps({
    active: { type: Boolean, default: false },
    title: { type: String, required: true },
    // [{ index, network, ip, reason }]; reason is null for a usable one
    interfaces: { type: Array, default: () => [] },
    // the text of the button that picks the interface
    submit: { type: String, default: 'Next' },
  });

  const emit = defineEmits(['update:active', 'pick']);

  const uid = useId();
  const picked = ref(null);
  const modal = ref(null);

  watch(
    () => props.active,
    (on) => {
      if (on) {
        picked.value =
          props.interfaces.find((iface) => !iface.reason)?.index ?? null;
      }
    },
    { immediate: true },
  );

  function pick() {
    if (picked.value === null) return;
    emit('pick', picked.value);
    emit('update:active', false);
  }
</script>

<template>
  <b-modal
    ref="modal"
    :model-value="active"
    has-modal-card
    @update:model-value="(on) => emit('update:active', on)">
    <form class="modal-card vnc-interface-card" @submit.prevent="pick">
      <header class="modal-card-head">
        <h2 class="modal-card-title">{{ title }}</h2>
      </header>
      <section class="modal-card-body">
        <p :id="`${uid}-label`" class="label">Interface</p>
        <div
          class="vnc-interface-choices"
          role="radiogroup"
          :aria-labelledby="`${uid}-label`">
          <div
            v-for="iface in interfaces"
            :key="iface.index"
            class="vnc-interface-choice">
            <b-radio
              v-model="picked"
              :name="`${uid}-iface`"
              :native-value="iface.index"
              :disabled="Boolean(iface.reason)"
              :aria-describedby="
                iface.reason ? `${uid}-reason-${iface.index}` : undefined
              ">
              {{ iface.index }}: {{ iface.network
              }}<template v-if="iface.ip"> · {{ iface.ip }}</template>
            </b-radio>
            <p
              v-if="iface.reason"
              :id="`${uid}-reason-${iface.index}`"
              class="help">
              {{ iface.reason }}
            </p>
          </div>
        </div>
      </section>
      <footer class="modal-card-foot buttons is-right">
        <button class="button" type="button" @click="modal.cancel('x')">
          Cancel
        </button>
        <button
          class="button is-success vnc-interface-pick"
          type="submit"
          :disabled="picked === null">
          {{ submit }}
        </button>
      </footer>
    </form>
  </b-modal>
</template>

<style scoped>
  .vnc-interface-card {
    width: min(28rem, calc(100vw - 2rem));
  }

  .vnc-interface-choices {
    display: grid;
    gap: 0.5rem;
  }

  .vnc-interface-choice .help {
    margin: 0 0 0 1.75rem;
  }
</style>
