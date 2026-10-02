<!--
  Custom icons, opened by the Custom icon field of a device or a group (see
  InspectorIconControl).

  It lists the icons the diagram carries and those of the user's own icon
  library on the server, and gives the field the one chosen with Use (the
  `use` event, with the icon's id, name and data). Upload icon… takes a PNG,
  JPEG, GIF, WebP or SVG file: the browser draws it and the PNG that results
  is what is sent (see rasterizeIcon in icons.js); the library keeps and
  returns PNGs only, and the icon shown is the one the server answered with.
  An icon of the diagram can be saved to the library, and one of the library
  deleted, after a confirmation: diagrams that use it keep their own copy.

  Every icon is drawn through BuilderIcon, as an <img> with a PNG data URL.
  The thumbnails are decoration: each row names its icon in text, and every
  button names the icon it acts on.

  The status and error regions are rendered, empty, from the start. After an
  upload focus moves to the new icon's Use, and after a delete to Upload
  icon…, so it is never left on a control that went.
-->
<template>
  <builder-dialog
    title="Custom icons"
    title-id="icon-dialog-title"
    class="builder-icons"
    data-testid="icon-dialog"
    @close="$emit('close')">
    <p class="builder-hint builder-icons__intro">
      An icon is a small image, drawn at 16 pixels. PNG, JPEG, GIF, WebP and SVG
      files are converted to a PNG of at most 96 by 96 pixels.
    </p>

    <button
      ref="uploadButton"
      type="button"
      class="builder-button"
      data-testid="icon-upload"
      :aria-busy="busy === 'upload' || undefined"
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
      <ul class="builder-icons__list" data-testid="icon-diagram-list">
        <li
          v-for="icon in diagram"
          :key="icon.id"
          class="builder-icons__row"
          :data-icon="icon.id">
          <builder-icon name="image" :src="srcOf(icon)" :size="32" />
          <span class="builder-icons__text">
            <span class="builder-icons__name">{{ nameOf(icon) }}</span>
            <span class="builder-hint">{{ iconSizeText(icon) }}</span>
          </span>
          <span class="builder-icons__buttons">
            <button
              type="button"
              class="builder-button"
              data-testid="icon-use"
              :aria-label="`Use ${nameOf(icon)}`"
              @click="use(icon)">
              Use
            </button>
            <!-- Whether the library has it is known once it is read. -->
            <template v-if="library">
              <span v-if="inLibrary(icon.id)" class="builder-hint">
                In my library
              </span>
              <button
                v-else
                type="button"
                class="builder-button"
                data-testid="icon-save"
                :aria-label="`Save ${nameOf(icon)} to my library`"
                :aria-disabled="busy ? 'true' : undefined"
                @click="save(icon)">
                Save to my library
              </button>
            </template>
          </span>
        </li>
      </ul>
    </template>

    <h3 class="builder-icons__heading" data-testid="icon-library-heading">
      {{ libraryHeading }}
    </h3>
    <p v-if="loading" class="builder-hint">Loading your library…</p>
    <div v-else-if="loadError" class="builder-icons__failed">
      <p class="builder-dialog__error" role="alert">{{ loadError }}</p>
      <button
        type="button"
        class="builder-button"
        data-testid="icon-retry"
        @click="load">
        Retry
      </button>
    </div>
    <p v-else-if="!library || !library.icons.length" class="builder-hint">
      Your library has no icons yet.
    </p>
    <ul v-else class="builder-icons__list" data-testid="icon-library-list">
      <li
        v-for="icon in library.icons"
        :key="icon.id"
        class="builder-icons__row"
        :data-icon="icon.id">
        <builder-icon name="image" :src="srcOf(icon)" :size="32" />
        <span class="builder-icons__text">
          <span class="builder-icons__name">{{ nameOf(icon) }}</span>
          <span class="builder-hint">{{ iconSizeText(icon) }}</span>
        </span>
        <span class="builder-icons__buttons">
          <button
            type="button"
            class="builder-button"
            data-testid="icon-use"
            :aria-label="`Use ${nameOf(icon)}`"
            @click="use(icon)">
            Use
          </button>
          <button
            type="button"
            class="builder-button builder-button--danger"
            data-testid="icon-delete"
            :aria-label="`Delete ${nameOf(icon)} from my library`"
            :aria-disabled="busy ? 'true' : undefined"
            @click="askDelete(icon)">
            Delete
          </button>
        </span>
      </li>
    </ul>

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
      :message="`Delete ${nameOf(deleting)} from your library? Diagrams that use it keep their copy.`"
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
    IconFileError,
    MAX_DOCUMENT_ICONS,
    iconSizeText,
    iconSrc,
    libraryTitle,
    rasterizeIcon,
    sortIcons,
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

  const icons = useInspectorIcons();
  // The user's icon library on the server (see iconLibrary.js).
  const server = useInspectorIconLibrary();
  const status = useMessage();
  const error = useMessage();

  const uploadButton = ref(null);
  const fileField = ref(null);
  // The library as the server last listed it, with the changes made here
  // since: null until it is read.
  const library = ref(null);
  const loading = ref(true);
  const loadError = ref('');
  // What is under way: 'upload', 'save' or 'delete', or ''. A busy button
  // keeps focus, so it can still be pressed, and is then ignored.
  const busy = ref('');
  // The library icon Delete asks about, or null.
  const deleting = ref(null);
  // Set once the dialog has gone: an answer that comes later changes
  // nothing.
  let closed = false;

  // The icons the diagram carries, in the order the library lists its own.
  const diagram = computed(() => sortIcons(icons.diagram()));

  const libraryHeading = computed(() => libraryTitle(library.value));

  function nameOf(icon) {
    return icon.name || 'Unnamed icon';
  }

  function srcOf(icon) {
    return iconSrc(icon.id, { [icon.id]: icon });
  }

  function inLibrary(id) {
    return Boolean(library.value?.icons.some((icon) => icon.id === id));
  }

  async function load() {
    loading.value = true;
    loadError.value = '';

    try {
      const read = await server.list();

      if (!closed) {
        library.value = { ...read, icons: sortIcons(read.icons) };
      }
    } catch (caught) {
      if (!closed) {
        loadError.value = server.failure(caught);
      }
    } finally {
      loading.value = false;
    }
  }

  // Puts an icon the server answered with into the list shown. A library
  // that could not be read before is read now.
  async function keep(icon) {
    const read = library.value;

    if (!read) {
      await load();

      return;
    }

    if (inLibrary(icon.id)) {
      return;
    }

    library.value = {
      ...read,
      icons: sortIcons([...read.icons, icon]),
      usedBytes: read.usedBytes + (icon.bytes || 0),
    };
  }

  // Focuses a row's button once the list shows the row.
  async function focusRow(list, id, testid) {
    await nextTick();
    document
      .querySelector(
        `[data-testid="${list}"] [data-icon="${CSS.escape(id)}"] [data-testid="${testid}"]`,
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
    busy.value = 'upload';

    try {
      const { icon, created } = await server.upload(
        await props.rasterize(file),
      );

      if (closed) {
        return;
      }

      await keep(icon);
      status.set(
        created
          ? `Added ${nameOf(icon)} to your library.`
          : `Your library already has this icon, as ${nameOf(icon)}.`,
      );
      await focusRow('icon-library-list', icon.id, 'icon-use');
    } catch (caught) {
      if (!closed) {
        error.set(
          caught instanceof IconFileError
            ? caught.message
            : server.failure(caught),
        );
      }
    } finally {
      busy.value = '';
    }
  }

  async function save(icon) {
    if (busy.value) {
      return;
    }

    status.clear();
    error.clear();
    busy.value = 'save';

    try {
      const answer = await server.upload({
        name: icon.name,
        data: icon.data,
      });

      if (closed) {
        return;
      }

      await keep(answer.icon);
      status.set(
        answer.created
          ? `Saved ${nameOf(answer.icon)} to your library.`
          : `Your library already has this icon, as ${nameOf(answer.icon)}.`,
      );
      // Save to my library went with the save.
      await focusRow('icon-diagram-list', icon.id, 'icon-use');
    } catch (caught) {
      if (!closed) {
        error.set(server.failure(caught));
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
      await server.remove(icon.id);

      if (closed) {
        return;
      }

      const read = library.value;

      library.value = {
        ...read,
        icons: read.icons.filter((entry) => entry.id !== icon.id),
        usedBytes: Math.max(0, read.usedBytes - (icon.bytes || 0)),
      };
      status.set(`Deleted ${nameOf(icon)} from your library.`);
      await nextTick();
      uploadButton.value?.focus();
    } catch (caught) {
      if (!closed) {
        error.set(server.failure(caught));
      }
    } finally {
      busy.value = '';
    }
  }

  function use(icon) {
    if (icons.full(icon.id)) {
      status.clear();
      error.set(
        `This diagram already has ${MAX_DOCUMENT_ICONS} custom icons. Remove one from a node first.`,
      );

      return;
    }

    emit('use', { id: icon.id, name: icon.name || '', data: icon.data });
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

  .builder-icons__text {
    display: flex;
    flex: 1 1 10rem;
    flex-direction: column;
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
