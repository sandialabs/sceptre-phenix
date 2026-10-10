<!--
  Move to a group, opened by the toolbar's Move to group and the palette's
  command: group membership without a drag. The node moves into the
  group's box, which grows to hold it, or out of the one it leaves.

  Node starts as the first selected node, and Group as the group that node
  is in now, so Move takes it elsewhere. A group is offered only when the
  node can go into it: not the node itself, nor a group inside it. Move with
  no node chosen says so and focuses Node; a move that would change nothing
  says why, and focus stays on Move. Once moved, the dialog closes, focus
  goes back to what opened it, and the live region, which waits while a
  dialog is open, says where the node went. The rules are in
  structureForms.js.
-->
<template>
  <builder-dialog
    title="Move to a group"
    title-id="regroup-dialog-title"
    class="builder-regroup-dialog"
    data-testid="regroup-dialog"
    @close="$emit('close')">
    <form novalidate @submit.prevent="moveToGroup">
      <div class="builder-field">
        <label for="regroup-node">Node</label>
        <select
          id="regroup-node"
          v-model="form.nodeId"
          :aria-invalid="missingNode || undefined"
          :aria-describedby="missingNode ? ERROR_ID : undefined"
          :disabled="store.readOnly">
          <option value="">Select a node</option>
          <option v-for="node in movable" :key="node.id" :value="node.id">
            {{ nodeNames.get(node.id) }}
          </option>
        </select>
      </div>

      <div class="builder-field">
        <label for="regroup-group">Group</label>
        <select
          id="regroup-group"
          v-model="form.groupId"
          :disabled="store.readOnly">
          <option value="">No group</option>
          <option v-for="node in groupChoices" :key="node.id" :value="node.id">
            {{ groupNames.get(node.id) }}
          </option>
        </select>
      </div>

      <!-- Rendered empty from the start; a new key replaces the message, so
           a repeated one is announced again. -->
      <p
        :id="ERROR_ID"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.seq" data-testid="regroup-error">{{
          error.text
        }}</span>
      </p>

      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="regroup-dialog-cancel"
          @click="$emit('close')">
          Cancel
        </button>
        <button
          type="submit"
          class="builder-button builder-button--primary"
          :disabled="store.readOnly"
          data-testid="regroup-dialog-submit">
          <builder-icon name="group" :size="14" />
          Move
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { nextTick } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import { useRegroupForm } from './structureForms.js';

  import { useBuilderStore } from '@/builder/store.js';

  const ERROR_ID = 'regroup-error';

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const {
    form,
    error,
    movable,
    groupChoices,
    nodeNames,
    groupNames,
    missingNode,
    submit,
  } = useRegroupForm(store);

  async function moveToGroup() {
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
  .builder-regroup-dialog {
    width: min(30rem, 92vw);
  }
</style>
