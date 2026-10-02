<!--
  A collection of the user's template library: a named group of its
  templates. Opened on the drafts page's Node Templates tab by New
  collection, by "New collection…" in the Add to collection menu (the
  selected templates are then its first members), and by Edit collection.

  It edits the collection's name and description; which templates it holds
  is changed on the tab (Add to collection, Remove from collection). What
  keeps it from being saved shows in the dialog, which stays open with what
  was typed. A collection that changed since the dialog opened (another tab)
  is not replaced unasked: the dialog says so, and the next Save replaces
  its name and description, and leaves the templates it holds now. Saving is
  announced by the page, once the dialog has closed.
-->
<template>
  <builder-dialog
    :title="title"
    title-id="collection-dialog-title"
    class="builder-collection-dialog"
    data-testid="collection-dialog"
    @close="requestClose">
    <form novalidate @submit.prevent="save">
      <p
        v-if="!collection && templateIds.length"
        class="builder-hint builder-collection-dialog__intro"
        data-testid="collection-members">
        {{
          templateIds.length === 1
            ? 'The selected template is added to it.'
            : `The ${templateIds.length} selected templates are added to it.`
        }}
      </p>

      <div class="builder-field">
        <label for="collection-name"
          >Name<span aria-hidden="true"> *</span></label
        >
        <input
          id="collection-name"
          ref="nameField"
          v-model="name"
          type="text"
          required
          autocomplete="off"
          :aria-invalid="invalid('name')"
          :aria-describedby="describedBy('name', 'collection-name-hint')"
          data-testid="collection-name" />
        <!-- Rendered from the start, so it is read when it appears. -->
        <p
          id="collection-name-hint"
          class="builder-hint"
          role="status"
          data-testid="collection-name-hint">
          {{ duplicate ? 'Another collection has this name.' : '' }}
        </p>
      </div>
      <div class="builder-field">
        <label for="collection-description">Description</label>
        <input
          id="collection-description"
          v-model="description"
          type="text"
          autocomplete="off"
          :aria-invalid="invalid('description')"
          :aria-describedby="describedBy('description')"
          data-testid="collection-description" />
      </div>

      <p
        id="collection-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="collection-error">
          {{ error.text }}
        </span>
      </p>

      <div class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="collection-cancel"
          @click="requestClose">
          Cancel
        </button>
        <!-- While the library saves it, it keeps focus and does nothing. -->
        <button
          type="submit"
          class="builder-button builder-button--primary"
          :aria-disabled="saving || undefined"
          :aria-busy="saving || undefined"
          data-testid="collection-save">
          {{ saving ? 'Saving…' : saveLabel }}
        </button>
      </div>
    </form>
  </builder-dialog>
</template>

<script setup>
  import { computed, onMounted, ref } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import { useFieldError } from './message.js';

  import { count } from '@/builder/announce.js';
  import { preconditionETag } from '@/builder/api.js';
  import { useBuilderStore } from '@/builder/store.js';
  import { collectionProblem, templateText } from '@/builder/templates.js';

  const CHANGED_ELSEWHERE =
    'This collection was changed in another tab or window. Save again to replace that version, or Cancel to keep it.';

  const props = defineProps({
    // The collection to edit, as the library lists it; null for a new one.
    collection: { type: Object, default: null },
    // The templates a new collection starts with, by id.
    templateIds: { type: Array, default: () => [] },
  });

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const { error, invalid, describedBy, fail } = useFieldError(
    'collection-error',
    { name: 'collection-name', description: 'collection-description' },
  );

  const nameField = ref(null);
  const name = ref(props.collection?.name || '');
  const description = ref(props.collection?.description || '');
  const saving = ref(false);

  const title = props.collection
    ? `Edit collection ${props.collection.name}`
    : 'New collection';
  const saveLabel = props.collection ? 'Save' : 'Create collection';

  // The collection as the library has it now: read again after a refused
  // save, it is what the next Save replaces.
  const listed = computed(
    () =>
      store.ownCollections.find((entry) => entry.id === props.collection?.id) ||
      props.collection,
  );
  // The tag it is saved against: as it opened, until a save is refused.
  let etag = props.collection?.etag || '';

  // A name another collection of the library has is allowed, and said.
  const duplicate = computed(() => {
    const typed = templateText(name.value).toLowerCase();

    return (
      typed !== '' &&
      store.ownCollections.some(
        (entry) =>
          entry.id !== props.collection?.id &&
          templateText(entry.name).toLowerCase() === typed,
      )
    );
  });

  // Cancel, Escape, the Close button and a click outside the dialog. The
  // save under way decides whether the dialog closes.
  function requestClose() {
    if (!saving.value) {
      emit('close');
    }
  }

  async function send(content) {
    if (!props.collection) {
      await store.createCollection({
        ...content,
        templateIds: props.templateIds,
      });
      store.announce(
        props.templateIds.length
          ? `Created collection ${content.name} with ${count(props.templateIds.length, 'template')}.`
          : `Created collection ${content.name}.`,
      );

      return;
    }

    await store.updateCollection(
      props.collection.id,
      { ...content, templateIds: listed.value.templateIds || [] },
      etag,
    );
    store.announce(`Updated collection ${content.name}.`);
  }

  async function save() {
    if (saving.value) {
      return;
    }

    error.clear();

    // On one line, as a template's name and description are saved.
    const content = {
      name: templateText(name.value),
      description: templateText(description.value),
    };
    const problem = collectionProblem(content);

    if (problem) {
      await fail(problem.message, problem.field);

      return;
    }

    const most = store.templates.limits.collections;

    if (!props.collection && store.ownCollections.length >= most) {
      await fail(`Your library has ${most} collections, the most it can hold.`);

      return;
    }

    saving.value = true;

    try {
      await send(content);
      emit('close');
    } catch (failure) {
      // Changed since it was read: the refusal carries the tag it has now,
      // and the library, read again, the templates it holds now.
      if (failure?.response?.status === 412) {
        await store.fetchTemplates();
        etag = preconditionETag(failure) || listed.value.etag || etag;
        await fail(CHANGED_ELSEWHERE);
      } else {
        await fail(store.describeLibraryError(failure, 'save the collection'));
      }
    } finally {
      saving.value = false;
    }
  }

  onMounted(() => {
    nameField.value?.focus();
    nameField.value?.select();
  });
</script>

<style scoped>
  .builder-collection-dialog {
    width: min(30rem, 92vw);
  }

  .builder-collection-dialog .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-collection-dialog .builder-hint:empty {
    margin: 0;
  }

  .builder-collection-dialog .builder-collection-dialog__intro {
    margin: 0 0 0.6rem;
  }
</style>
