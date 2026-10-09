<!--
  Add a connection, opened by the toolbar's Add connection and the palette's
  command: a device's interface joined to a switch, without a drag.

  The fields start from the selection: a selected device fills Device, and a
  selected switch fills Switch. Interface offers the device's free
  interfaces, a handle taking part in one connection only, as the server
  requires, or a new one. Connect with a field empty names the fields still
  empty, marks them and focuses the first; a kind the diagram has none of is
  named as one to add first. A connection the store refuses says why here,
  and the dialog stays open. Once connected, the dialog closes, focus goes
  back to what opened it, and the live region, which waits while a dialog is
  open, says the nodes were connected. The rules are in structureForms.js.
-->
<template>
  <builder-dialog
    title="Add a connection"
    title-id="connect-dialog-title"
    class="builder-connect-dialog"
    data-testid="connect-dialog"
    @close="$emit('close')">
    <form novalidate @submit.prevent="connect">
      <div class="builder-field">
        <label for="connect-device">Device</label>
        <select
          id="connect-device"
          v-model="form.deviceId"
          :aria-invalid="missingDevice || undefined"
          :aria-describedby="missingDevice ? ERROR_ID : undefined"
          :disabled="store.readOnly">
          <option value="">Select a device</option>
          <option v-for="node in devices" :key="node.id" :value="node.id">
            {{ nodeLabel(node) }}
          </option>
        </select>
      </div>

      <div class="builder-field">
        <label for="connect-interface">Interface</label>
        <select
          id="connect-interface"
          v-model="form.handleId"
          :disabled="store.readOnly">
          <option value="">Add a new interface</option>
          <option
            v-for="handle in availableHandles"
            :key="handle.id"
            :value="handle.id">
            {{ handle.name }}
          </option>
        </select>
      </div>

      <div class="builder-field">
        <label for="connect-switch">Switch</label>
        <select
          id="connect-switch"
          v-model="form.switchId"
          :aria-invalid="missingSwitch || undefined"
          :aria-describedby="missingSwitch ? ERROR_ID : undefined"
          :disabled="store.readOnly">
          <option value="">Select a switch</option>
          <option v-for="node in switches" :key="node.id" :value="node.id">
            {{ switchLabel(node) }}
          </option>
        </select>
      </div>

      <!-- Rendered empty from the start; a new key replaces the message, so
           a repeated one is announced again. Its text follows the fields:
           once a device is chosen it asks only for the switch. -->
      <p
        :id="ERROR_ID"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="message" :key="error.seq" data-testid="connect-error">{{
          message
        }}</span>
      </p>

      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="connect-dialog-cancel"
          @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          :disabled="store.readOnly"
          data-testid="connect-dialog-submit">
          <builder-icon name="link" :size="14" />
          Connect
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { nextTick } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import { useConnectForm } from './structureForms.js';

  import { nodeLabel } from '@/builder/model.js';
  import { useBuilderStore } from '@/builder/store.js';

  const ERROR_ID = 'connect-error';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const {
    form,
    error,
    devices,
    switches,
    availableHandles,
    missingDevice,
    missingSwitch,
    message,
    switchLabel,
    submit,
  } = useConnectForm(store);

  async function connect() {
    const { done, focus } = submit();

    if (done) {
      emit('close');

      return;
    }

    if (focus) {
      await nextTick();
      document.getElementById(focus)?.focus();
    }
  }
</script>

<style scoped>
  .builder-connect-dialog {
    width: min(30rem, 92vw);
  }
</style>
