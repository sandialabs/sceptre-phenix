<!--
  Inspector.

  Entirely schema driven: the form is generated from the builder schema served
  by GET /api/v1/schemas/builder/v1 (with a bundled fallback), so fields the
  server adds show up without touching this component.

  The form edits a *working copy*. Nothing reaches the document until Apply is
  pressed, and Apply stays disabled while JSON Forms reports validation errors,
  so a half-typed hostname never becomes a history commit (and therefore never
  becomes a server snapshot).
-->
<template>
  <section
    class="builder-inspector builder-panel"
    aria-labelledby="inspector-title">
    <h2 id="inspector-title" class="builder-inspector__title">Inspector</h2>

    <p v-if="!target" class="builder-inspector__empty">
      Select a node or connection to edit its properties.
    </p>

    <template v-else>
      <p class="builder-inspector__subject">
        {{ target.title }}
        <span class="builder-inspector__source">
          schema: {{ store.schemaSource }}
        </span>
      </p>

      <p
        v-if="store.schemaError"
        class="builder-inspector__schema-error"
        role="alert"
        data-testid="inspector-schema-error">
        {{ store.schemaError }}
      </p>

      <form @submit.prevent="apply">
        <json-forms
          :key="formKey"
          :data="draft"
          :schema="schema"
          :uischema="uiSchema"
          :renderers="renderers"
          :ajv="validator"
          validation-mode="ValidateAndShow"
          @change="onChange" />

        <p
          v-if="errors.length"
          class="builder-inspector__errors"
          role="alert"
          data-testid="inspector-errors">
          {{ errors.length }} field
          {{ errors.length === 1 ? 'needs' : 'need' }} attention before these
          changes can be applied.
        </p>

        <div class="builder-inspector__actions">
          <button
            type="submit"
            class="builder-button builder-button--primary"
            data-testid="inspector-apply"
            :disabled="!canApply"
            :aria-disabled="!canApply">
            <builder-icon name="check" :size="14" />
            Apply
          </button>
          <button
            type="button"
            class="builder-button"
            data-testid="inspector-cancel"
            :disabled="!dirty"
            :aria-disabled="!dirty"
            @click="cancel">
            <builder-icon name="close" :size="14" />
            Cancel
          </button>
          <span class="builder-inspector__state" aria-live="polite">
            {{ stateText }}
          </span>
        </div>
      </form>

      <div v-if="target.kind === 'device'" class="builder-inspector__ifaces">
        <h3>Interfaces</h3>
        <ul>
          <li v-for="handle in target.interfaces" :key="handle.id">
            <span>{{ handle.name }} — {{ networkFor(handle.id) }}</span>
            <button
              type="button"
              class="builder-button builder-button--danger"
              :aria-label="`Remove interface ${handle.name}`"
              @click="store.removeInterface(target.target.id, handle.id)">
              <builder-icon name="trash" :size="12" />
            </button>
          </li>
        </ul>
        <button
          type="button"
          class="builder-button"
          data-testid="inspector-add-interface"
          @click="store.addInterface(target.target.id, {})">
          <builder-icon name="plus" :size="14" />
          Add interface
        </button>
      </div>
    </template>

    <div v-if="issues.length" class="builder-inspector__issues">
      <h3>Diagram checks</h3>
      <ul class="builder-issues">
        <li
          v-for="(issue, index) in issues"
          :key="`${issue.path}-${index}`"
          :data-level="issue.level">
          <strong>{{ issue.level === 'error' ? 'Error' : 'Warning' }}:</strong>
          {{ issue.message }}
        </li>
      </ul>
    </div>
  </section>
</template>

<script setup>
  import { computed, ref, watch } from 'vue';
  import { JsonForms } from '@jsonforms/vue';
  import { vanillaRenderers } from '@jsonforms/vue-vanilla';

  import BuilderIcon from './BuilderIcon.vue';

  import {
    applyFormData,
    formErrors,
    inspectorTarget,
    uiSchemaForKind,
  } from '@/builder/adapters/forms.js';
  import { createFormValidator } from '@/builder/form-validator.js';
  import { findNetwork } from '@/builder/model.js';
  import { schemaForKind } from '@/builder/schema.js';
  import { useBuilderStore } from '@/builder/store.js';

  const store = useBuilderStore();
  const renderers = Object.freeze([...vanillaRenderers]);
  const validator = createFormValidator();

  const draft = ref({});
  const errors = ref([]);
  const dirty = ref(false);

  const selection = computed(() => store.inspectorSelection);
  const target = computed(() => inspectorTarget(store.doc, selection.value));
  const formKey = computed(
    () => `${selection.value.type}-${selection.value.id || 'document'}`,
  );

  const schema = computed(() =>
    schemaForKind(store.schema, target.value?.kind || 'document', {
      spec: target.value?.data?.spec,
    }),
  );
  const uiSchema = computed(() =>
    uiSchemaForKind(store.schema, target.value?.kind || 'document', {
      spec: target.value?.data?.spec,
    }),
  );

  const issues = computed(() => store.issues);
  const canApply = computed(
    () => dirty.value && errors.value.length === 0 && !store.readOnly,
  );

  const stateText = computed(() => {
    if (store.readOnly) {
      return 'Read only';
    }

    if (errors.value.length > 0) {
      return 'Fix the highlighted fields';
    }

    return dirty.value ? 'Unapplied changes' : 'No changes';
  });

  function reset() {
    draft.value = target.value
      ? JSON.parse(JSON.stringify(target.value.data))
      : {};
    errors.value = [];
    dirty.value = false;
  }

  watch(formKey, reset, { immediate: true });

  // Reset when the document changes underneath us (undo, outline edits) unless
  // the user has unapplied work in progress, which must never be discarded
  // silently.
  watch(
    () => store.doc,
    () => {
      if (!dirty.value) {
        reset();
      }
    },
  );

  function onChange(event) {
    if (!event) {
      return;
    }

    errors.value = formErrors(event.errors);

    const next = JSON.stringify(event.data ?? {});

    if (next === JSON.stringify(draft.value)) {
      return;
    }

    draft.value = event.data;
    dirty.value = next !== JSON.stringify(target.value?.data ?? {});
  }

  function apply() {
    if (!canApply.value) {
      return;
    }

    const next = applyFormData(store.doc, selection.value, draft.value);

    store.commit(next, `Updated ${target.value.kind}`);
    dirty.value = false;
  }

  function cancel() {
    reset();
    store.announce('Discarded unapplied changes.');
  }

  function networkFor(handleId) {
    const edge = (store.doc.edges || []).find(
      (entry) =>
        entry.sourceHandleId === handleId || entry.targetHandleId === handleId,
    );

    if (!edge) {
      return 'not connected';
    }

    const network = findNetwork(store.doc, edge.networkId);

    return network ? `network ${network.name}` : 'unknown network';
  }

  defineExpose({ apply, cancel, draft, errors });
</script>

<style scoped>
  .builder-inspector__title {
    font-weight: 700;
    font-size: 0.9rem;
    margin: 0 0 0.35rem;
  }

  .builder-inspector__subject {
    font-size: 0.85rem;
    margin-bottom: 0.5rem;
  }

  .builder-inspector__source {
    display: block;
    font-size: 0.72rem;
    opacity: 0.8;
  }

  .builder-inspector__actions {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin: 0.5rem 0;
    flex-wrap: wrap;
  }

  .builder-inspector__state {
    font-size: 0.72rem;
    opacity: 0.85;
  }

  .builder-inspector__errors,
  .builder-inspector__schema-error {
    font-size: 0.78rem;
    margin: 0.35rem 0;
  }

  .builder-inspector__ifaces ul {
    list-style: none;
    margin: 0 0 0.4rem;
    padding: 0;
  }

  .builder-inspector__ifaces li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem;
    font-size: 0.8rem;
  }

  .builder-inspector__ifaces h3,
  .builder-inspector__issues h3 {
    font-weight: 700;
    font-size: 0.85rem;
    margin: 0.75rem 0 0.25rem;
  }
</style>
