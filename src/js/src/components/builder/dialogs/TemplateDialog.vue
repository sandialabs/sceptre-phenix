<!-- The template editor: the Inspector (variant 'template') on a document that
     holds the template's one device (see templateEditorHost in templates.js). -->
<template>
  <builder-dialog
    :title="title"
    title-id="template-dialog-title"
    :describedby="origin ? 'template-origin' : ''"
    class="builder-template-editor"
    data-testid="template-dialog"
    @close="requestClose">
    <p
      v-if="origin"
      id="template-origin"
      class="builder-template-editor__origin"
      data-testid="template-origin">
      {{ origin }}
    </p>
    <!-- tabindex: Firefox makes a box that scrolls a Tab stop of its own.
         This box holds fields, which Tab reaches and scrolls into view. -->
    <div ref="body" class="builder-template-editor__body" tabindex="-1">
      <!-- No heading of its own: its labels say what it holds, and its
           name says the same to a screen reader. -->
      <form
        :id="FORM_ID"
        class="builder-template-editor__about"
        aria-label="Template name and description"
        novalidate
        @submit.prevent="save">
        <div class="builder-field">
          <label for="template-name"
            >Template Name<span v-if="!viewing" aria-hidden="true">
              *</span
            ></label
          >
          <input
            id="template-name"
            ref="nameField"
            v-model="name"
            type="text"
            :required="!viewing"
            :readonly="viewing"
            autocomplete="off"
            :aria-invalid="invalid('name')"
            :aria-describedby="describedBy('name', 'template-name-hint')"
            data-testid="template-name" />
          <!-- Rendered from the start, so it is read when it appears. -->
          <p
            id="template-name-hint"
            class="builder-hint"
            role="status"
            data-testid="template-name-hint">
            {{ duplicate ? 'Another template has this name.' : '' }}
          </p>
        </div>
        <div class="builder-field">
          <label for="template-description">Template Description</label>
          <input
            id="template-description"
            v-model="description"
            type="text"
            :readonly="viewing"
            autocomplete="off"
            :aria-invalid="invalid('description')"
            :aria-describedby="
              describedBy('description', 'template-description-hint')
            "
            data-testid="template-description" />
          <p id="template-description-hint" class="builder-hint">
            The Nodes &amp; Templates section shows this text as a tooltip. The
            template does not copy it to the node.
          </p>
        </div>
      </form>

      <div class="builder-template-editor__fields">
        <h3 id="template-fields-title">Node fields</h3>
        <builder-inspector
          ref="inspector"
          :host="host"
          variant="template"
          labelledby="template-fields-title" />
      </div>
    </div>

    <p class="builder-visually-hidden" role="status">
      <span v-if="status.text" :key="status.key" data-testid="template-status">
        {{ status.text }}
      </span>
    </p>

    <div class="builder-template-editor__footer">
      <p
        id="template-error"
        class="builder-dialog__message builder-dialog__error"
        role="alert">
        <span v-if="error.text" :key="error.key" data-testid="template-error">
          {{ error.text }}
        </span>
      </p>

      <!-- Both rows stay in the document, one of them shown, so focus can
           go back to the button that asked. -->
      <div
        v-show="confirming"
        class="builder-dialog__actions"
        role="group"
        aria-labelledby="template-discard-question"
        data-testid="template-discard">
        <p
          id="template-discard-question"
          class="builder-template-editor__question">
          Discard changes to this template?
        </p>
        <button
          ref="keepButton"
          type="button"
          class="builder-button"
          aria-describedby="template-discard-question"
          data-testid="template-keep"
          @click="keepEditing">
          Keep editing
        </button>
        <button
          type="button"
          class="builder-button builder-button--danger"
          data-testid="template-discard-confirm"
          @click="$emit('close')">
          Discard
        </button>
      </div>
      <div v-show="!confirming" class="builder-dialog__actions">
        <button
          type="button"
          class="builder-button"
          data-testid="template-cancel"
          @click="requestClose">
          {{ viewing ? 'Close' : 'Cancel' }}
        </button>
        <!-- While the library saves it, a ring turns over its label, and it
             keeps focus and does nothing. Viewing, it copies, which Enter in
             a field does not do. -->
        <builder-busy-button
          v-if="!viewing"
          type="submit"
          :form="FORM_ID"
          class="builder-button--primary"
          :busy="saving"
          data-testid="template-save">
          {{ MODES[mode].save }}
        </builder-busy-button>
        <builder-busy-button
          v-else-if="template.source !== 'library'"
          class="builder-button--primary"
          :busy="saving"
          data-testid="template-save"
          @click="copy">
          {{ MODES[mode].save }}
        </builder-busy-button>
      </div>
    </div>
  </builder-dialog>
</template>

<script>
  // What each way of opening the editor calls it, and its Save. `library`
  // marks a template of the library, not of the open diagram.
  const MODES = {
    'diagram-new': {
      title: () => 'New device template',
      save: 'Save to diagram',
    },
    'diagram-edit': {
      title: (template) => `Edit template ${template.name}`,
      save: 'Save',
    },
    'library-new': {
      title: () => 'New library template',
      save: 'Save to library',
      library: true,
    },
    'library-edit': {
      title: (template) => `Edit template ${template.name}`,
      save: 'Save',
      library: true,
    },
    // A read-only template, which the user can duplicate but not change.
    view: {
      title: (template) => `Template ${template.name}`,
      save: 'Duplicate to library',
      view: true,
    },
  };

  const CHANGED_ELSEWHERE =
    'This template changed after you opened it, by another user or in another window. To replace that version, select Save again. To keep it, select Cancel. Then open the template again.';
</script>

<script setup>
  import { computed, nextTick, onMounted, reactive, ref } from 'vue';

  import BuilderBusyButton from '../BuilderBusyButton.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderInspector from '../BuilderInspector.vue';
  import { useKeepOpenForTip } from '../fixedTooltip.js';
  import { useFieldError, useMessage } from './message.js';

  import { preconditionETag } from '@/builder/api.js';
  import { isTextEntry } from '@/builder/commands.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    copiedMessage,
    copyProblem,
    templateDocument,
    templateEditorHost,
    templateFromDocument,
    templateOrigin,
    templateProblem,
    templateText,
  } from '@/builder/templates.js';

  const FORM_ID = 'template-about';

  const props = defineProps({
    // 'diagram-new', 'diagram-edit' (`template` has an id), 'library-new',
    // 'library-edit' (`template` has an id and etag) or 'view' (read only).
    mode: {
      type: String,
      required: true,
      validator: (value) => Object.hasOwn(MODES, value),
    },
    // What the editor starts from: {id?, name, description?, device}.
    template: { type: Object, required: true },
    // The copies of icons kept where the template is, by name: the
    // diagram's. The library keeps none: its templates' icons are the icon
    // library's.
    icons: { type: Object, default: null },
    // The icon size of the diagram that the template is in. The template's
    // device draws at this size while it names no size of its own. Empty for
    // a template of the library, whose devices draw at the size of the
    // diagram they are added to (see templateDocument).
    iconSize: { type: String, default: '' },
    // The layers the template's device can be at: the diagram's `layers`,
    // or for a template of the library, those new diagrams start with.
    // Empty for the placeholder layers (see templateDocument).
    layers: { type: Array, default: () => [] },
    // The match conflict rule that goes with those layers. Empty for the
    // lowest layer ID.
    matchConflict: { type: String, default: '' },
  });

  const emit = defineEmits(['close']);

  const store = useBuilderStore();
  const status = useMessage();
  const { error, invalid, describedBy, fail } = useFieldError(
    'template-error',
    { name: 'template-name', description: 'template-description' },
  );

  const body = ref(null);
  const nameField = ref(null);
  const inspector = ref(null);
  const keepButton = ref(null);
  const name = ref(props.template.name || '');
  const description = ref(props.template.description || '');
  const confirming = ref(false);
  // The library is saving the template.
  const saving = ref(false);

  const title = MODES[props.mode].title(props.template);
  const inLibrary = Boolean(MODES[props.mode].library);
  const viewing = Boolean(MODES[props.mode].view);
  // The line under the title of a template that opens read only: whose it
  // is. Empty when the editor can change the template.
  const origin = viewing ? templateOrigin(props.template) : '';
  // The tag a template of the library is saved against: the one it was
  // listed with, then the one a refused save said it has now.
  let etag = props.template.etag || '';

  // What the Inspector edits: the template's one device, in a document of
  // the dialog's own.
  const host = reactive(
    templateEditorHost({
      doc: templateDocument(props.template, props.icons, {
        iconSize: props.iconSize,
        layers: props.layers,
        matchConflict: props.matchConflict,
      }),
      source: store,
      announce: (message) => status.set(message),
      readOnly: viewing,
      library: iconLibrary,
    }),
  );

  // The template as the editor's document has it: its device with the
  // form's applied edits, under the name and description typed.
  function current() {
    const { template } = templateFromDocument(host.doc);
    const text = templateText(description.value);

    return {
      template: {
        name: templateText(name.value),
        ...(text ? { description: text } : {}),
        device: template.device,
      },
    };
  }

  // What it was when the editor opened, which Cancel compares with.
  const opened = JSON.stringify(current().template);

  function changed() {
    return (
      Boolean(inspector.value?.changed) ||
      JSON.stringify(current().template) !== opened
    );
  }

  // The names of the other templates kept where this one is: in the
  // diagram, or in the library.
  const others = computed(() =>
    (inLibrary ? store.libraryTemplates : store.doc.templates || [])
      .filter((template) => template.id !== props.template.id)
      .map((template) => templateText(template.name).toLowerCase()),
  );
  const duplicate = computed(() => {
    const typed = templateText(name.value).toLowerCase();

    return !viewing && typed !== '' && others.value.includes(typed);
  });

  // --- closing ---------------------------------------------------------

  // Where focus was when the dialog asked whether to discard changes.
  let asked = null;

  // Cancel, Escape, the Close button and a click outside the dialog. With
  // changes, it asks first. Asked again, it keeps editing, as Escape
  // cancels a question.
  async function requestClose() {
    // The save in progress decides whether the dialog closes.
    if (saving.value) {
      return;
    }

    if (confirming.value) {
      keepEditing();

      return;
    }

    if (viewing || !changed()) {
      emit('close');

      return;
    }

    asked = document.activeElement;
    confirming.value = true;
    await nextTick();
    keepButton.value?.focus();
  }

  async function keepEditing() {
    confirming.value = false;
    await nextTick();

    (asked?.isConnected && body.value?.parentElement?.contains(asked)
      ? asked
      : nameField.value
    )?.focus();
    asked = null;
  }

  useKeepOpenForTip(() =>
    Boolean(body.value?.querySelector('.builder-tooltip--fixed')),
  );

  // --- saving ----------------------------------------------------------

  // The form's summary of the fields that need attention: its first entry
  // takes focus, and names the field.
  function focusErrors() {
    body.value?.querySelector('.builder-inspector__error-link')?.focus();
  }

  // Saves a template of the library, and says so after the dialog closes.
  // Resolves to whether it was saved. The dialog says why not.
  async function saveToLibrary(template) {
    saving.value = true;
    status.set('Saving the template…');

    try {
      if (props.mode === 'library-edit') {
        await store.updateLibraryTemplate(props.template.id, template, etag);
        store.announce(`Updated template ${template.name}.`);
      } else {
        await store.createLibraryTemplates([template]);
        store.announce(`Saved ${template.name} to the library.`);
      }

      return true;
    } catch (failure) {
      // Changed since it was read. The refusal carries the tag it has now, and
      // so does the library, read again.
      if (failure?.response?.status === 412) {
        await store.fetchTemplates();
        etag =
          preconditionETag(failure) ||
          store.libraryTemplates.find((entry) => entry.id === props.template.id)
            ?.etag ||
          etag;
        await fail(CHANGED_ELSEWHERE);
      } else {
        await fail(store.describeLibraryError(failure, 'save the template'));
      }

      return false;
    } finally {
      saving.value = false;
      status.clear();
    }
  }

  // A read-only template, viewed, goes to the library as a copy, naming the
  // custom icon it names. The page says so after the dialog closes. The
  // dialog shows a failure.
  async function copy() {
    if (saving.value) {
      return;
    }

    error.clear();

    const full = copyProblem(store.templates, 1);

    if (full) {
      await fail(`Could not duplicate the template. ${full}`);

      return;
    }

    saving.value = true;
    status.set('Duplicating the template…');

    try {
      await store.createLibraryTemplates([props.template]);
      store.announce(copiedMessage([props.template]));
      emit('close');
    } catch (failure) {
      await fail(store.describeLibraryError(failure, 'duplicate the template'));
    } finally {
      saving.value = false;
      status.clear();
    }
  }

  async function save() {
    if (confirming.value || saving.value || viewing) {
      return;
    }

    error.clear();

    // A field of the node's form being typed in commits what it holds, as
    // its change event would.
    const field = document.activeElement;

    if (isTextEntry(field) && field.closest('.builder-inspector')) {
      field.dispatchEvent(new Event('change', { bubbles: true }));
      await nextTick();
    }

    if (await inspector.value?.settle()) {
      await nextTick();
      focusErrors();

      return;
    }

    const { template } = current();
    const problem = templateProblem(template);

    if (problem) {
      await fail(problem.message, problem.field);

      return;
    }

    if (inLibrary) {
      if (await saveToLibrary(template)) {
        emit('close');
      }

      return;
    }

    // One edit of the diagram, which the page announces after the dialog
    // closes. A change names the description even when it is emptied,
    // which removes it.
    const saved =
      props.mode === 'diagram-edit'
        ? store.updateTemplate(props.template.id, {
            description: '',
            ...template,
          })
        : store.addTemplate(template);

    if (!saved) {
      await fail(
        'Could not save the template. The Builder cannot change the diagram now.',
      );

      return;
    }

    emit('close');
  }

  onMounted(() => {
    // The drive image field suggests the server's disk images, and warns
    // of one the server lacks, as on the canvas.
    store.fetchDisks();
    nameField.value?.focus();
    nameField.value?.select();
  });
</script>

<style scoped>
  /* A large part of the window, so the form has room, or all of a
     narrow window. The body scrolls, and the footer stays in view. */
  .builder-template-editor[open] {
    display: flex;
    flex-direction: column;
    width: min(96vw, 1200px);
    max-width: none;
    height: min(92vh, 900px);
    max-height: none;
    /* Clip, not hidden: a script that scrolls a field into view cannot
       scroll the dialog, so the title stays in view. */
    overflow: hidden;
    overflow: clip;
  }

  @media (max-width: 699px) {
    .builder-template-editor[open] {
      width: 100vw;
      height: 100vh;
      height: 100dvh;
      margin: 0;
      border: 0;
      border-radius: 0;
    }
  }

  .builder-template-editor__origin {
    flex: none;
    margin: 0 0 0.75rem;
    color: var(--bx-text-muted);
    overflow-wrap: anywhere;
  }

  /* Relative, so text hidden from sight inside it is clipped by it and does
     not make the dialog overflow. */
  .builder-template-editor__body {
    position: relative;
    display: grid;
    flex: 1 1 auto;
    grid-template-columns: minmax(0, 1fr);
    align-content: start;
    gap: 0.5rem 1.5rem;
    min-height: 0;
    padding-right: 0.25rem;
    overflow-y: auto;
  }

  /* Two columns in a window with room for them: the template's own fields
     stay in view beside the node's, which scroll. */
  @media (min-width: 900px) {
    .builder-template-editor__body {
      grid-template-columns: 20rem minmax(0, 1fr);
    }

    .builder-template-editor__about {
      position: sticky;
      top: 0;
      align-self: start;
    }
  }

  .builder-template-editor h3 {
    margin: 0 0 0.5rem;
    font-size: 0.95rem;
    font-weight: 700;
  }

  .builder-template-editor .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-template-editor .builder-hint:empty {
    margin: 0;
  }

  .builder-template-editor__fields {
    min-width: 0;
  }

  .builder-template-editor__footer {
    flex: none;
    margin-top: 0.75rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--bx-border);
  }

  .builder-template-editor__footer .builder-dialog__actions {
    align-items: center;
  }

  .builder-template-editor__question {
    flex: 1 1 12rem;
    margin: 0;
    font-weight: 600;
  }
</style>
