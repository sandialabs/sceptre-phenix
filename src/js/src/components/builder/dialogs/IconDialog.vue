<!--
  Custom icons, opened by the Custom icon field of a device or a group (see
  InspectorIconControl).

  It lists the server's icons, which every user shares, and the copies of
  icons the diagram carries, and gives the field the name of the one chosen
  with Use (the `use` event, with the icon's name). Upload icon… takes a PNG,
  JPEG, GIF, WebP or SVG file: the browser draws it and the PNG that results
  is what is sent (see rasterizeIcon in icons.js), under a name the user
  types (proposed from the file's name). A copy the diagram carries can be
  added to the server under its name. A server icon is renamed or deleted
  only by the user who uploaded it, or with the builder-icons permissions:
  Rename and Delete show only when the server says the user may. A renamed
  icon keeps its old name as another name, so diagrams that use it keep
  showing it; a deleted one leaves them showing their built-in icon.

  Every icon is drawn through BuilderIcon, as an <img> with a PNG data URL.
  The thumbnails are decoration: each row names its icon in text, and every
  button names the icon it acts on.

  The status and error regions are rendered, empty, from the start. Focus
  moves to the new icon's Use after an upload, to the renamed icon's Rename
  after a rename, to Upload icon… after a delete, and back to the button
  that opened a form when the form closes, so it is never left on a control
  that went.
-->
<template>
  <builder-dialog
    title="Custom icons"
    title-id="icon-dialog-title"
    class="builder-icons"
    data-testid="icon-dialog"
    @close="$emit('close')">
    <p class="builder-hint builder-icons__intro">
      An icon is a small image that every user of this server can use, drawn at
      16, 24 or 32 pixels as the node's icon size says. PNG, JPEG, GIF, WebP and
      SVG files are converted to a PNG of at most 96 by 96 pixels.
    </p>

    <button
      v-if="!adding"
      ref="uploadButton"
      type="button"
      class="builder-button"
      data-testid="icon-upload"
      :aria-busy="busy === 'convert' || undefined"
      :aria-disabled="busy ? 'true' : undefined"
      @click="chooseFile">
      <builder-icon name="upload" :size="14" />
      Upload icon…
    </button>
    <!-- The button above is the Tab stop. The field's input and change are
         the dialog's own, not those of a form the dialog is opened in. -->
    <input
      ref="fileField"
      type="file"
      hidden
      tabindex="-1"
      aria-hidden="true"
      :accept="ACCEPT"
      data-testid="icon-file"
      @input.stop
      @change.stop="onFile" />

    <form
      v-if="adding"
      class="builder-icons__form"
      data-testid="icon-upload-form"
      aria-labelledby="icon-upload-title"
      @submit.prevent="submitUpload">
      <h3 id="icon-upload-title" class="builder-icons__heading">
        Name the new icon
      </h3>
      <div class="builder-icons__row">
        <builder-icon name="image" :src="srcOf(adding)" :size="32" />
        <span class="builder-icons__field">
          <label for="icon-upload-name">Name</label>
          <input
            id="icon-upload-name"
            ref="uploadName"
            v-model="uploadNameText"
            class="builder-input"
            type="text"
            required
            maxlength="64"
            autocomplete="off"
            spellcheck="false"
            aria-describedby="icon-upload-hint"
            :aria-invalid="uploadNameError ? 'true' : undefined"
            data-testid="icon-upload-name"
            @input.stop
            @change.stop />
          <span id="icon-upload-hint" class="builder-hint">
            Nodes and templates name the icon by it. {{ ICON_NAME_HINT }}
          </span>
        </span>
      </div>
      <div class="builder-icons__buttons">
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="icon-upload-submit"
          :aria-disabled="busy ? 'true' : undefined">
          Add icon
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="icon-upload-cancel"
          @click="cancelUpload">
          Cancel
        </button>
      </div>
    </form>

    <p class="builder-dialog__message" role="status">
      <span v-if="status.text" :key="status.key" data-testid="icon-status">{{
        status.text
      }}</span>
    </p>
    <p class="builder-dialog__message builder-dialog__error" role="alert">
      <span v-if="error.text" :key="error.key" data-testid="icon-error">{{
        error.text
      }}</span>
    </p>

    <template v-if="diagram.length">
      <h3 class="builder-icons__heading">
        In this diagram ({{ diagram.length }})
      </h3>
      <p class="builder-hint">
        Copies the diagram carries, from a file it was uploaded from.
      </p>
      <ul class="builder-icons__list" data-testid="icon-diagram-list">
        <li
          v-for="icon in diagram"
          :key="icon.name"
          class="builder-icons__row"
          :data-icon="icon.name">
          <builder-icon name="image" :src="srcOf(icon)" :size="32" />
          <span class="builder-icons__text">
            <span class="builder-icons__name">{{ icon.name }}</span>
            <span class="builder-hint">{{ iconSizeText(icon) }}</span>
          </span>
          <span class="builder-icons__buttons">
            <button
              type="button"
              class="builder-button"
              data-testid="icon-use"
              :aria-label="`Use ${icon.name}`"
              @click="use(icon)">
              Use
            </button>
            <span
              v-if="onServer(icon)"
              class="builder-hint"
              data-testid="icon-on-server">
              On the server
            </span>
            <button
              v-else-if="library.state.status === 'ready'"
              type="button"
              class="builder-button"
              data-testid="icon-add-to-server"
              :aria-label="`Add ${icon.name} to the server`"
              :aria-disabled="busy ? 'true' : undefined"
              @click="addToServer(icon)">
              Add to server
            </button>
          </span>
        </li>
      </ul>
    </template>

    <h3 class="builder-icons__heading" data-testid="icon-library-heading">
      Server icons ({{ library.state.icons.length }})
    </h3>
    <p v-if="usage" class="builder-hint" data-testid="icon-usage">
      {{ usage }}
    </p>

    <form
      v-if="renaming"
      class="builder-icons__form"
      data-testid="icon-rename-form"
      aria-labelledby="icon-rename-title"
      @submit.prevent="submitRename">
      <h3 id="icon-rename-title" class="builder-icons__heading">
        Rename {{ renaming.name }}
      </h3>
      <span class="builder-icons__field">
        <label for="icon-rename-name">New name</label>
        <input
          id="icon-rename-name"
          ref="renameName"
          v-model="renameText"
          class="builder-input"
          type="text"
          required
          maxlength="64"
          autocomplete="off"
          spellcheck="false"
          aria-describedby="icon-rename-hint"
          data-testid="icon-rename-name"
          @input.stop
          @change.stop />
        <span id="icon-rename-hint" class="builder-hint">
          The old name, {{ renaming.name }}, keeps working as another name of
          this icon, so diagrams and templates that use it keep showing it.
          {{ ICON_NAME_HINT }}
        </span>
      </span>
      <div class="builder-icons__buttons">
        <button
          type="submit"
          class="builder-button builder-button--primary"
          data-testid="icon-rename-submit"
          :aria-disabled="busy ? 'true' : undefined">
          Rename
        </button>
        <button
          type="button"
          class="builder-button"
          data-testid="icon-rename-cancel"
          @click="cancelRename">
          Cancel
        </button>
      </div>
    </form>

    <p
      v-if="library.state.status === 'loading' && !library.state.icons.length"
      class="builder-hint">
      Loading the server's icons…
    </p>
    <div
      v-else-if="
        library.state.status === 'failed' && !library.state.icons.length
      "
      class="builder-icons__failed">
      <p class="builder-dialog__error" role="alert">
        {{ library.state.error }}
      </p>
      <button
        type="button"
        class="builder-button"
        data-testid="icon-retry"
        @click="load">
        Retry
      </button>
    </div>
    <p v-else-if="!library.state.icons.length" class="builder-hint">
      The server has no icons yet.
    </p>
    <template v-else>
      <div class="builder-icons__filter">
        <label for="icon-filter">Filter icons</label>
        <input
          id="icon-filter"
          v-model="filterText"
          class="builder-input"
          type="search"
          autocomplete="off"
          spellcheck="false"
          aria-describedby="icon-filter-count"
          data-testid="icon-filter"
          @input.stop
          @change.stop />
        <span id="icon-filter-count" class="builder-hint">
          {{ filterCount }}
        </span>
      </div>
      <ul class="builder-icons__list" data-testid="icon-library-list">
        <li
          v-for="icon in shown"
          :key="icon.name"
          class="builder-icons__row"
          :data-icon="icon.name">
          <builder-icon name="image" :src="srcOf(icon)" :size="32" />
          <span class="builder-icons__text">
            <span class="builder-icons__name">{{ icon.name }}</span>
            <span class="builder-hint">{{ detailsOf(icon) }}</span>
          </span>
          <span class="builder-icons__buttons">
            <button
              type="button"
              class="builder-button"
              data-testid="icon-use"
              :aria-label="`Use ${icon.name}`"
              @click="use(icon)">
              Use
            </button>
            <button
              v-if="icon.canRename"
              type="button"
              class="builder-button"
              data-testid="icon-rename"
              :aria-label="`Rename ${icon.name}`"
              :aria-disabled="busy ? 'true' : undefined"
              @click="askRename(icon, $event)">
              Rename
            </button>
            <button
              v-if="icon.canDelete"
              type="button"
              class="builder-button builder-button--danger"
              data-testid="icon-delete"
              :aria-label="`Delete ${icon.name} from the server`"
              :aria-disabled="busy ? 'true' : undefined"
              @click="askDelete(icon)">
              Delete
            </button>
          </span>
        </li>
      </ul>
    </template>

    <div class="builder-dialog__actions">
      <button
        type="button"
        class="builder-button"
        data-testid="icon-close"
        @click="$emit('close')">
        Close
      </button>
    </div>

    <builder-confirm
      v-if="deleting"
      id="icon-delete"
      title="Delete icon?"
      :message="`Delete ${deleting.name} from the server? Diagrams and templates that use it, by any of its names, will show their built-in icon instead.`"
      confirm-label="Delete"
      @cancel="deleting = null"
      @confirm="confirmDelete" />
  </builder-dialog>
</template>

<script setup>
  import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';

  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import {
    useInspectorIconLibrary,
    useInspectorIcons,
  } from '../inspector/control.js';
  import { useMessage } from './message.js';

  import {
    ICON_NAME_HINT,
    IconFileError,
    iconSizeText,
    iconSrc,
    isIconName,
    rasterizeIcon,
    sortIcons,
    usageText,
  } from '@/builder/icons.js';

  const props = defineProps({
    // Makes an icon of a chosen file (see rasterizeIcon).
    rasterize: { type: Function, default: rasterizeIcon },
  });

  const emit = defineEmits(['close', 'use']);

  // The files the file chooser offers. What makes a file an image is that
  // the browser can draw it, not its name or its type.
  const ACCEPT =
    '.png,.jpg,.jpeg,.gif,.webp,.svg,image/png,image/jpeg,image/gif,image/webp,image/svg+xml';

  // What a refused name says beside the name rule.
  const BAD_NAME = 'Enter a name for the icon.';

  const icons = useInspectorIcons();
  // The server's icon library (see iconLibrary.js).
  const library = useInspectorIconLibrary();
  const status = useMessage();
  const error = useMessage();

  const uploadButton = ref(null);
  const fileField = ref(null);
  const uploadName = ref(null);
  const renameName = ref(null);
  // The converted image waiting for its name ({data}), or null.
  const adding = ref(null);
  const uploadNameText = ref('');
  const uploadNameError = ref(false);
  // The server icon being renamed, the new name typed, and the button that
  // opened the form, which takes focus back when it closes.
  const renaming = ref(null);
  const renameText = ref('');
  let renameOpener = null;
  const filterText = ref('');
  // What is under way: 'convert', 'upload', 'add', 'rename' or 'delete', or
  // ''. A busy button keeps focus, so it can still be pressed, and is then
  // ignored.
  const busy = ref('');
  // The server icon Delete asks about, or null.
  const deleting = ref(null);
  // Set once the dialog has gone: an answer that comes later changes
  // nothing.
  let closed = false;

  // The copies the diagram carries, by name.
  const diagram = computed(() => sortIcons(icons.diagram()));

  const usage = computed(() => usageText(library.state));

  const shown = computed(() => {
    const words = filterText.value.trim().toLowerCase();

    if (!words) {
      return library.state.icons;
    }

    return library.state.icons.filter((icon) =>
      [icon.name, icon.owner, ...(icon.aliases || [])].some((text) =>
        String(text || '')
          .toLowerCase()
          .includes(words),
      ),
    );
  });

  const filterCount = computed(() =>
    filterText.value.trim()
      ? `${shown.value.length} of ${library.state.icons.length} icons shown.`
      : '',
  );

  function srcOf(icon) {
    const key = isIconName(icon?.name) ? icon.name : 'new-icon';

    return iconSrc(key, { [key]: icon });
  }

  function detailsOf(icon) {
    const parts = [
      icon.owner ? `Uploaded by ${icon.owner}` : '',
      iconSizeText(icon),
      icon.aliases?.length ? `also named ${icon.aliases.join(', ')}` : '',
    ];

    return parts.filter(Boolean).join(' · ');
  }

  // Whether the server has a copy as it is, under its name or an alias.
  function onServer(icon) {
    return library.lookup(icon.name)?.data === icon.data;
  }

  async function load() {
    try {
      await library.load();
    } catch {
      // The library's state says why.
    }
  }

  // Focuses a row's button once the list shows the row.
  async function focusRow(list, name, testid) {
    await nextTick();
    document
      .querySelector(
        `[data-testid="${list}"] [data-icon="${CSS.escape(name)}"] [data-testid="${testid}"]`,
      )
      ?.focus();
  }

  function chooseFile() {
    if (!busy.value) {
      fileField.value?.click();
    }
  }

  async function onFile(event) {
    const field = event.target;
    const file = field.files?.[0];

    // The same file chosen again is a choice again.
    field.value = '';

    if (!file || busy.value) {
      return;
    }

    status.clear();
    error.clear();
    busy.value = 'convert';

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
        error.set(
          caught instanceof IconFileError
            ? caught.message
            : library.failure(caught),
        );
      }
    } finally {
      busy.value = '';
    }
  }

  async function cancelUpload() {
    adding.value = null;
    uploadNameError.value = false;
    await nextTick();
    uploadButton.value?.focus();
  }

  // Why a typed name cannot be sent, or ''.
  function nameProblem(name) {
    return isIconName(name) ? '' : `${BAD_NAME} ${ICON_NAME_HINT}`;
  }

  async function submitUpload() {
    const name = uploadNameText.value.trim();

    if (busy.value || !adding.value) {
      return;
    }

    status.clear();
    error.clear();

    const problem = nameProblem(name);

    if (problem) {
      uploadNameError.value = true;
      error.set(problem);
      uploadName.value?.focus();

      return;
    }

    busy.value = 'upload';

    try {
      const { icon, created } = await library.upload({
        name,
        data: adding.value.data,
      });

      if (closed) {
        return;
      }

      adding.value = null;
      uploadNameError.value = false;
      status.set(
        created
          ? `Added ${icon.name} to the server.`
          : `The server already has this icon as ${icon.name}.`,
      );
      await focusRow('icon-library-list', icon.name, 'icon-use');
    } catch (caught) {
      if (!closed) {
        // A taken name asks for another; the form stays.
        uploadNameError.value = true;
        error.set(library.failure(caught));
        uploadName.value?.focus();
      }
    } finally {
      busy.value = '';
    }
  }

  async function addToServer(icon) {
    if (busy.value) {
      return;
    }

    status.clear();
    error.clear();
    busy.value = 'add';

    try {
      const answer = await library.upload({ name: icon.name, data: icon.data });

      if (closed) {
        return;
      }

      status.set(
        answer.created
          ? `Added ${answer.icon.name} to the server. The diagram drops its copy with its next edit.`
          : `The server already has this icon as ${answer.icon.name}.`,
      );
      // Add to server went with the upload.
      await focusRow('icon-diagram-list', icon.name, 'icon-use');
    } catch (caught) {
      if (!closed) {
        error.set(library.failure(caught));
      }
    } finally {
      busy.value = '';
    }
  }

  async function askRename(icon, event) {
    if (busy.value) {
      return;
    }

    status.clear();
    error.clear();
    renameOpener = event?.currentTarget || null;
    renaming.value = icon;
    renameText.value = icon.name;
    await nextTick();
    renameName.value?.focus();
    renameName.value?.select();
  }

  async function cancelRename() {
    const opener = renameOpener;

    renaming.value = null;
    renameOpener = null;
    await nextTick();
    opener?.focus();
  }

  async function submitRename() {
    const icon = renaming.value;
    const name = renameText.value.trim();

    if (busy.value || !icon) {
      return;
    }

    status.clear();
    error.clear();

    const problem = nameProblem(name);

    if (problem) {
      error.set(problem);
      renameName.value?.focus();

      return;
    }

    if (name === icon.name) {
      await cancelRename();

      return;
    }

    busy.value = 'rename';

    try {
      const renamed = await library.rename(icon.name, name);

      if (closed) {
        return;
      }

      renaming.value = null;
      renameOpener = null;
      filterText.value = '';
      status.set(
        `Renamed ${icon.name} to ${renamed?.name || name}. ${icon.name} keeps working as another name of it.`,
      );
      await focusRow('icon-library-list', renamed?.name || name, 'icon-rename');
    } catch (caught) {
      if (!closed) {
        error.set(library.failure(caught));
        renameName.value?.focus();
      }
    } finally {
      busy.value = '';
    }
  }

  function askDelete(icon) {
    if (!busy.value) {
      deleting.value = icon;
    }
  }

  async function confirmDelete() {
    const icon = deleting.value;

    deleting.value = null;

    if (!icon || busy.value) {
      return;
    }

    status.clear();
    error.clear();
    busy.value = 'delete';

    try {
      await library.remove(icon.name);

      if (closed) {
        return;
      }

      status.set(`Deleted ${icon.name} from the server.`);
      await nextTick();
      uploadButton.value?.focus();
    } catch (caught) {
      if (!closed) {
        error.set(library.failure(caught));
      }
    } finally {
      busy.value = '';
    }
  }

  function use(icon) {
    emit('use', { name: icon.name });
  }

  onMounted(load);

  onBeforeUnmount(() => {
    closed = true;
  });
</script>

<style scoped>
  .builder-icons {
    width: min(38rem, 92vw);
  }

  .builder-icons .builder-icons__intro {
    margin: 0 0 0.6rem;
  }

  .builder-icons__heading {
    margin: 0.9rem 0 0.4rem;
    font-size: 0.95rem;
    font-weight: 700;
  }

  .builder-icons__list {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* A row wraps in a narrow window: the buttons go under the name. */
  .builder-icons__row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem 0.6rem;
    padding: 0.35rem 0;
    border-top: 1px solid var(--bx-border);
  }

  .builder-icons__text,
  .builder-icons__field {
    display: flex;
    flex: 1 1 10rem;
    flex-direction: column;
    gap: 0.2rem;
    min-width: 0;
  }

  .builder-icons__name {
    overflow-wrap: anywhere;
  }

  .builder-icons__buttons {
    display: flex;
    flex: none;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  .builder-icons__form {
    margin: 0.6rem 0;
    padding: 0.6rem;
    border: 1px solid var(--bx-border);
    border-radius: 6px;
  }

  .builder-icons__form .builder-icons__buttons {
    margin-top: 0.5rem;
  }

  .builder-icons__filter {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    margin-bottom: 0.4rem;
  }

  .builder-icons__failed {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
  }

  .builder-icons__failed p {
    margin: 0;
  }

  .builder-icons .builder-dialog__actions {
    margin-top: 0.9rem;
  }
</style>
