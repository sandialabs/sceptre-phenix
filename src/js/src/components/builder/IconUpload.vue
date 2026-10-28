<!-- Upload icon…: the button, file chooser and name form (see rasterizeIcon in
     icons.js). The parent owns `busy` and the messages. `prefix` starts the ids,
     so two can be on one page. -->
<template>
  <!-- While a file is read, a ring turns in place of the icon. -->
  <builder-busy-button
    v-if="!adding"
    ref="uploadButton"
    icon="upload"
    :data-testid="`${prefix}-upload`"
    :busy="busy === 'convert'"
    :disabled="Boolean(busy)"
    @click="chooseFile">
    Upload icon…
  </builder-busy-button>
  <!-- The button above is the Tab stop. The field's input and change are
       this component's own, not those of a form it is in. -->
  <input
    ref="fileField"
    type="file"
    hidden
    tabindex="-1"
    aria-hidden="true"
    :accept="ICON_FILE_ACCEPT"
    :data-testid="`${prefix}-file`"
    @input.stop
    @change.stop="onFile" />

  <form
    v-if="adding"
    class="icon-upload__form"
    :data-testid="`${prefix}-upload-form`"
    :aria-labelledby="`${prefix}-upload-title`"
    @submit.prevent="submitUpload">
    <h3 :id="`${prefix}-upload-title`" class="icon-upload__heading">
      Name the new icon
    </h3>
    <div class="icon-upload__row">
      <builder-icon name="image" :src="srcOf(adding)" :size="32" />
      <span class="builder-field icon-upload__field">
        <label :for="`${prefix}-upload-name`">Name</label>
        <input
          :id="`${prefix}-upload-name`"
          ref="uploadName"
          v-model="uploadNameText"
          class="builder-input"
          type="text"
          required
          maxlength="64"
          autocomplete="off"
          spellcheck="false"
          :aria-describedby="`${prefix}-upload-hint`"
          :aria-invalid="uploadNameError ? 'true' : undefined"
          :data-testid="`${prefix}-upload-name`"
          @input.stop
          @change.stop />
        <span :id="`${prefix}-upload-hint`" class="builder-hint">
          Nodes and templates name the icon by it. {{ ICON_NAME_HINT }}
        </span>
      </span>
    </div>
    <div class="icon-upload__buttons">
      <!-- While the server takes the icon, a ring turns over its label. -->
      <builder-busy-button
        type="submit"
        class="builder-button--primary"
        :data-testid="`${prefix}-upload-submit`"
        :busy="busy === 'upload'"
        :disabled="Boolean(busy)">
        Add icon
      </builder-busy-button>
      <button
        type="button"
        class="builder-button"
        :data-testid="`${prefix}-upload-cancel`"
        @click="cancelUpload">
        Cancel
      </button>
    </div>
  </form>
</template>

<script>
  // The files the file chooser offers. What makes a file an image is that
  // the browser can draw it, not its name or its type.
  const ICON_FILE_ACCEPT =
    '.png,.jpg,.jpeg,.gif,.webp,.svg,image/png,image/jpeg,image/gif,image/webp,image/svg+xml';

  // What a refused name says beside the name rule.
  const BAD_NAME = 'Enter a name for the icon.';
</script>

<script setup>
  import { nextTick, onBeforeUnmount, ref } from 'vue';

  import BuilderBusyButton from './BuilderBusyButton.vue';
  import BuilderIcon from './BuilderIcon.vue';

  import {
    ICON_NAME_HINT,
    IconFileError,
    iconSrc,
    isIconName,
    rasterizeIcon,
  } from '@/builder/icons.js';

  const props = defineProps({
    // The server's icon library (see createIconLibrary in iconLibrary.js).
    library: { type: Object, required: true },
    // What is in progress, '' for nothing (v-model:busy).
    busy: { type: String, default: '' },
    // The parent's status and error messages (see useMessage).
    status: { type: Object, required: true },
    error: { type: Object, required: true },
    // Makes an icon of a chosen file (see rasterizeIcon).
    rasterize: { type: Function, default: rasterizeIcon },
    prefix: { type: String, default: 'icon' },
  });

  const emit = defineEmits(['update:busy', 'uploaded']);

  const uploadButton = ref(null);
  const fileField = ref(null);
  const uploadName = ref(null);
  // The converted image waiting for its name ({data}), or null.
  const adding = ref(null);
  const uploadNameText = ref('');
  const uploadNameError = ref(false);
  // Set when the parent closes. An answer that comes later changes nothing.
  let closed = false;

  // What is in progress is the parent's, which this sets at once, so a
  // second press in the same task sees it.
  let working = '';

  function setBusy(value) {
    working = value;
    emit('update:busy', value);
  }

  function isBusy() {
    return Boolean(props.busy || working);
  }

  function srcOf(icon) {
    const key = isIconName(icon?.name) ? icon.name : 'new-icon';

    return iconSrc(key, { [key]: icon });
  }

  function chooseFile() {
    if (!isBusy()) {
      fileField.value?.click();
    }
  }

  async function onFile(event) {
    const field = event.target;
    const file = field.files?.[0];

    // The same file chosen again is a choice again.
    field.value = '';

    if (!file || isBusy()) {
      return;
    }

    props.status.clear();
    props.error.clear();
    setBusy('convert');

    try {
      const icon = await props.rasterize(file);

      if (closed) {
        return;
      }

      adding.value = { data: icon.data };
      uploadNameText.value = icon.name;
      uploadNameError.value = false;
      await nextTick();
      uploadName.value?.focus();
      uploadName.value?.select();
    } catch (caught) {
      if (!closed) {
        props.error.set(
          caught instanceof IconFileError
            ? caught.message
            : props.library.failure(caught),
        );
      }
    } finally {
      setBusy('');
    }
  }

  async function cancelUpload() {
    adding.value = null;
    uploadNameError.value = false;
    await nextTick();
    uploadButton.value?.focus();
  }

  async function submitUpload() {
    const name = uploadNameText.value.trim();

    if (isBusy() || !adding.value) {
      return;
    }

    props.status.clear();
    props.error.clear();

    if (!isIconName(name)) {
      uploadNameError.value = true;
      props.error.set(`${BAD_NAME} ${ICON_NAME_HINT}`);
      uploadName.value?.focus();

      return;
    }

    setBusy('upload');

    try {
      const answer = await props.library.upload({
        name,
        data: adding.value.data,
      });

      if (closed) {
        return;
      }

      adding.value = null;
      uploadNameError.value = false;
      props.status.set(
        answer.created
          ? `Added ${answer.icon.name} to the server.`
          : `The server already has this icon as ${answer.icon.name}.`,
      );
      emit('uploaded', answer);
    } catch (caught) {
      if (!closed) {
        // A taken name asks for another. The form stays.
        uploadNameError.value = true;
        props.error.set(props.library.failure(caught));
        uploadName.value?.focus();
      }
    } finally {
      setBusy('');
    }
  }

  onBeforeUnmount(() => {
    closed = true;
  });

  defineExpose({
    // Focuses Upload icon…, after the button that had focus went away.
    focus: () => uploadButton.value?.focus(),
  });
</script>

<style scoped>
  .icon-upload__form {
    margin: 0.6rem 0;
    padding: 0.6rem;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
  }

  .icon-upload__heading {
    margin: 0 0 0.4rem;
    font-size: 0.95rem;
    font-weight: 700;
  }

  .icon-upload__row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem 0.6rem;
  }

  .icon-upload__field {
    display: flex;
    margin-bottom: 0;
    flex: 1 1 10rem;
    flex-direction: column;
    gap: 0.2rem;
    min-width: 0;
  }

  .icon-upload__buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-top: 0.5rem;
  }
</style>
