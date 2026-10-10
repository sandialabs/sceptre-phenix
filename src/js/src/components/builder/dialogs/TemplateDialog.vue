<!--
  The template editor: a device template's name and description, and the
  fields of the device it makes. These controls open it:

  - the "+" beside the palette's Device templates heading (a new template
    of the diagram, from the selected device or from a plain one)
  - Edit in the menu of a template of the diagram
  - on the drafts page's Node Templates tab, New template and a card's
    Edit, for a template of the user's library.

  The node fields are the Inspector itself (BuilderInspector.vue, variant
  'template'), on a document of the dialog's own that holds the template's
  one device (see templateDocument and templateEditorHost in templates.js).
  The schema, the form, its checks and its renderers are the canvas's, with
  room to lay the fields out in columns. Nothing reaches the open diagram
  until Save, which is one edit of it.

  Save does these steps:

  1. It applies the form's edits to that document, or moves focus to the
     form's error summary.
  2. It checks the name, the description and the size of the template.
  3. It saves the template.

  Cancel, Escape or a click outside the dialog asks before it drops changes.
  What the form announces goes to a status region of the dialog, because
  the page's live region cannot be heard through a modal dialog. The page
  announces the save after the dialog closes.

  The server saves a template of the library. While it does, Save says so
  and the dialog stays open. A failure shows in the dialog, which keeps
  what was typed, so the user can press Save again. The dialog does not
  replace a template that changed since the editor opened (another tab)
  without asking. It says so, and the next Save replaces it.

  Another user's template, shared with the user or published server-wide,
  opens read only from its card's View. Every field is locked, but readable
  and reachable with Tab. A line under the title says whose template it is.
  Copy to my library, for a role that may add templates, adds a copy that
  the user owns.
-->
<template>
  <builder-dialog
    :title="title"
    title-id="template-dialog-title"
    :describedby="viewing ? 'template-origin' : ''"
    class="builder-template-editor"
    data-testid="template-dialog"
    @close="requestClose">
    <p
      v-if="viewing"
      id="template-origin"
      class="builder-template-editor__origin"
      data-testid="template-origin">
      {{ templateOrigin(template) }}
    </p>
    <!-- tabindex: Firefox makes a box that scrolls a Tab stop of its own.
         This box holds fields, which Tab reaches and scrolls into view. -->
    <div ref="body" class="builder-template-editor__body" tabindex="-1">
      <form
        :id="FORM_ID"
        class="builder-template-editor__about"
        aria-labelledby="template-about-title"
        novalidate
        @submit.prevent="save">
        <h3 id="template-about-title">Template</h3>
        <div class="builder-field">
          <label for="template-name"
            >Name<span v-if="!viewing" aria-hidden="true"> *</span></label
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
          <label for="template-description">Description</label>
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
            Shown as a tooltip in Add nodes. It is not written to the node.
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
        <!-- While the library saves it, it keeps focus and does nothing.
             Viewing, it copies, for a role that may add templates, which
             Enter in a field does not do. -->
        <button
          v-if="!viewing"
          type="submit"
          :form="FORM_ID"
          class="builder-button builder-button--primary"
          :aria-disabled="saving || undefined"
          :aria-busy="saving || undefined"
          data-testid="template-save">
          {{ saving ? 'Saving…' : MODES[mode].save }}
        </button>
        <button
          v-else-if="store.templateRights.create"
          type="button"
          class="builder-button builder-button--primary"
          :aria-disabled="saving || undefined"
          :aria-busy="saving || undefined"
          data-testid="template-save"
          @click="copy">
          {{ saving ? 'Copying…' : MODES[mode].save }}
        </button>
      </div>
    </div>
  </builder-dialog>
</template>

<script>
  // What each way of opening the editor calls it, and its Save. `library`
  // marks a template of the user's library, not of the open diagram.
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
    // Another user's template, which the user can copy but not change.
    view: {
      title: (template) => `Template ${template.name}`,
      save: 'Copy to my library',
      view: true,
    },
  };

  const CHANGED_ELSEWHERE =
    'This template was changed in another tab or window. Save again to replace that version, or Cancel to keep it.';
</script>

<script setup>
  import {
    computed,
    nextTick,
    onBeforeUnmount,
    onMounted,
    reactive,
    ref,
  } from 'vue';

  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderInspector from '../BuilderInspector.vue';
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
    // 'diagram-new': a new template of the open diagram.
    // 'diagram-edit': one the diagram has, which `template` then is, with its
    // id.
    // 'library-new': a new template of the user's library.
    // 'library-edit': one the library has, which `template` then is, with its
    // id and etag.
    // 'view': another user's template, as listed, read only.
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
  // The tag a template of the library is saved against: the one it was
  // listed with, then the one a refused save said it has now.
  let etag = props.template.etag || '';

  // What the Inspector edits: the template's one device, in a document of
  // the dialog's own.
  const host = reactive(
    templateEditorHost({
      doc: templateDocument(props.template, props.icons, {
        iconSize: props.iconSize,
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
  // diagram, or in the user's library.
  const others = computed(() =>
    (inLibrary ? store.ownTemplates : store.doc.templates || [])
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

  // Escape with a field's tooltip shown hides the tooltip only (WCAG
  // 1.4.13), and does not ask to close the dialog. This listener is added
  // before a tooltip's own, which then hides it.
  function keepOpenForTip(event) {
    if (
      event.key === 'Escape' &&
      body.value?.querySelector('.builder-tooltip--fixed')
    ) {
      event.preventDefault();
      event.stopPropagation();
    }
  }

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

    try {
      if (props.mode === 'library-edit') {
        await store.updateLibraryTemplate(props.template.id, template, etag);
        store.announce(`Updated template ${template.name}.`);
      } else {
        await store.createLibraryTemplates([template]);
        store.announce(`Saved ${template.name} to your library.`);
      }

      return true;
    } catch (failure) {
      // Changed since it was read. The refusal carries the tag it has now, and
      // so does the library, read again.
      if (failure?.response?.status === 412) {
        await store.fetchTemplates();
        etag =
          preconditionETag(failure) ||
          store.ownTemplates.find((entry) => entry.id === props.template.id)
            ?.etag ||
          etag;
        await fail(CHANGED_ELSEWHERE);
      } else {
        await fail(store.describeLibraryError(failure, 'save the template'));
      }

      return false;
    } finally {
      saving.value = false;
    }
  }

  // Another user's template, viewed, goes to the user's library as a copy
  // they own, naming the custom icon it names. The page says so after the
  // dialog closes. The dialog shows a failure.
  async function copy() {
    if (saving.value) {
      return;
    }

    error.clear();

    const full = copyProblem(store.templates, 1);

    if (full) {
      await fail(`Could not copy the template. ${full}`);

      return;
    }

    saving.value = true;

    try {
      await store.createLibraryTemplates([props.template]);
      store.announce(copiedMessage([props.template]));
      emit('close');
    } catch (failure) {
      await fail(store.describeLibraryError(failure, 'copy the template'));
    } finally {
      saving.value = false;
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
        'Could not save the template. The diagram cannot be changed right now.',
      );

      return;
    }

    emit('close');
  }

  onMounted(() => {
    window.addEventListener('keydown', keepOpenForTip, true);
    // The drive image field suggests the server's disk images, and warns
    // of one the server lacks, as on the canvas.
    store.fetchDisks();
    nameField.value?.focus();
    nameField.value?.select();
  });

  onBeforeUnmount(() => {
    window.removeEventListener('keydown', keepOpenForTip, true);
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
    overflow: hidden;
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

  .builder-template-editor__body {
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
