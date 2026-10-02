<!--
  The template editor: a device template's name and description, and the
  fields of the device it makes. Opened by the "+" beside the palette's
  Device templates heading (a new template of the diagram, from the selected
  device or from a plain one) and by Edit in the menu of a template of the
  diagram; and, on the drafts page's Node Templates tab, by New template and
  by a card's Edit, for a template of the user's library.

  The node fields are the Inspector itself (BuilderInspector.vue, variant
  'template'), on a document of the dialog's own that holds the template's
  one device (see templateDocument and templateEditorHost in templates.js):
  the schema, the form, its checks and its renderers are the canvas's, with
  room to lay the fields out in columns. Nothing reaches the open diagram
  until Save, which is one edit of it.

  Save applies the form's edits to that document, or moves focus to the
  form's error summary; then checks the name, the description and the size
  of the template; then saves it. Cancel, Escape or a click outside the
  dialog asks before it drops changes. What the form announces goes to a
  status region of the dialog, since the page's live region cannot be heard
  through a modal dialog; saving is announced by the page, once the dialog
  has closed.

  A template of the library is saved by the server. While it is, Save says
  so and the dialog stays; a failure is said in the dialog, which keeps
  what was typed, so Save can be pressed again. A template that changed
  since the editor opened (another tab) is not replaced unasked: the dialog
  says so, and the next Save replaces it.
-->
<template>
  <builder-dialog
    :title="title"
    title-id="template-dialog-title"
    class="builder-template-editor"
    data-testid="template-dialog"
    @close="requestClose">
    <!-- tabindex: Firefox makes a box that scrolls a Tab stop of its own;
         this one holds fields, which Tab reaches and scrolls into view. -->
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
            >Name<span aria-hidden="true"> *</span></label
          >
          <input
            id="template-name"
            ref="nameField"
            v-model="name"
            type="text"
            required
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
          Cancel
        </button>
        <!-- While the library saves it, it keeps focus and does nothing. -->
        <button
          type="submit"
          :form="FORM_ID"
          class="builder-button builder-button--primary"
          :aria-disabled="saving || undefined"
          :aria-busy="saving || undefined"
          data-testid="template-save">
          {{ saving ? 'Saving…' : MODES[mode].save }}
        </button>
      </div>
    </div>
  </builder-dialog>
</template>

<script>
  // What each way of opening the editor calls it, and its Save; `library`
  // for a template of the user's library, not of the open diagram.
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
  import { useBuilderStore } from '@/builder/store.js';
  import {
    templateDocument,
    templateEditorHost,
    templateFromDocument,
    templateProblem,
    templateText,
  } from '@/builder/templates.js';

  const FORM_ID = 'template-about';

  const props = defineProps({
    // 'diagram-new': a new template of the open diagram; 'diagram-edit':
    // one the diagram has, which `template` then is, with its id.
    // 'library-new': a new template of the user's library; 'library-edit':
    // one the library has, which `template` then is, with its id and etag.
    mode: {
      type: String,
      required: true,
      validator: (value) => Object.hasOwn(MODES, value),
    },
    // What the editor starts from: {id?, name, description?, device}.
    template: { type: Object, required: true },
    // The custom icons kept where the template is, by icon id: the
    // diagram's, or the library's.
    icons: { type: Object, default: null },
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
  // The tag a template of the library is saved against: the one it was
  // listed with, then the one a refused save said it has now.
  let etag = props.template.etag || '';

  // What the Inspector edits: the template's one device, in a document of
  // the dialog's own.
  const host = reactive(
    templateEditorHost({
      doc: templateDocument(props.template, props.icons),
      source: store,
      announce: (message) => status.set(message),
    }),
  );

  // The template as the editor's document has it: its device with the
  // form's applied edits, under the name and description typed.
  function current() {
    const { template, icons } = templateFromDocument(host.doc);
    const text = templateText(description.value);

    return {
      template: {
        name: templateText(name.value),
        ...(text ? { description: text } : {}),
        device: template.device,
      },
      icons,
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

    return typed !== '' && others.value.includes(typed);
  });

  // --- closing ---------------------------------------------------------

  // Where focus was when the dialog asked whether to discard changes.
  let asked = null;

  // Cancel, Escape, the Close button and a click outside the dialog. With
  // changes it asks first; asked again, it keeps editing, as Escape cancels
  // a question.
  async function requestClose() {
    // The save under way decides whether the dialog closes.
    if (saving.value) {
      return;
    }

    if (confirming.value) {
      keepEditing();

      return;
    }

    if (!changed()) {
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

  // Escape with a field's tooltip up hides the tooltip only (WCAG 1.4.13),
  // rather than ask to close the dialog too. This listener is added before
  // a tooltip's own, which then hides it.
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

  // Saves a template of the library, and says so once the dialog has
  // closed. Resolves to whether it was saved; why not is said in the dialog.
  async function saveToLibrary(template, icons) {
    saving.value = true;

    try {
      if (props.mode === 'library-edit') {
        await store.updateLibraryTemplate(
          props.template.id,
          template,
          etag,
          icons,
        );
        store.announce(`Updated template ${template.name}.`);
      } else {
        await store.createLibraryTemplates([template], { icons });
        store.announce(`Saved ${template.name} to your library.`);
      }

      return true;
    } catch (failure) {
      // Changed since it was read: the refusal carries the tag it has now,
      // and so does the library, read again.
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

  async function save() {
    if (confirming.value || saving.value) {
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

    const { template, icons } = current();
    const problem = templateProblem(template);

    if (problem) {
      await fail(problem.message, problem.field);

      return;
    }

    if (inLibrary) {
      if (await saveToLibrary(template, icons)) {
        emit('close');
      }

      return;
    }

    // One edit of the diagram, which the page announces once the dialog
    // has closed. A change names the description even when it is emptied,
    // which removes it.
    const saved =
      props.mode === 'diagram-edit'
        ? store.updateTemplate(
            props.template.id,
            { description: '', ...template },
            icons,
          )
        : store.addTemplate(template, icons);

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
  /* A large part of the window, so the form has room; all of it in a
     narrow one. The body scrolls, and the footer stays in view. */
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
