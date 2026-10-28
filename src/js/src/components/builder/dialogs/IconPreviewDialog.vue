<!-- The preview of one icon, at its stored size with sharp pixels and at the
     node sizes. It shows the tab's messages, because it makes the tab inert. -->
<template>
  <builder-dialog
    :title="`Icon ${name}`"
    title-id="icon-preview-title"
    class="icon-preview"
    data-testid="icon-preview-dialog"
    @close="$emit('close')">
    <div class="icon-preview__body">
      <figure v-if="icon.builtin" class="icon-preview__stored">
        <div class="icon-preview__frame">
          <builder-icon
            :name="icon.key"
            :size="LARGE_SIZE"
            :title="`${name} at ${LARGE_SIZE} pixels`"
            data-testid="icon-preview-image" />
        </div>
        <figcaption class="icon-preview__caption">
          Shown at {{ LARGE_SIZE }} × {{ LARGE_SIZE }} pixels
        </figcaption>
      </figure>
      <figure v-else class="icon-preview__stored">
        <div class="icon-preview__frame">
          <img
            v-if="src"
            class="icon-preview__pixels"
            :src="src"
            :width="size.width"
            :height="size.height"
            :alt="`${icon.name} at its stored size`"
            draggable="false"
            data-testid="icon-preview-image" />
        </div>
        <figcaption class="icon-preview__caption">
          Stored size: {{ sizeText }}
        </figcaption>
      </figure>

      <div class="icon-preview__nodes">
        <h3 id="icon-preview-nodes" class="icon-preview__heading">
          At node sizes
        </h3>
        <!-- The icon as nodes draw it, small, medium and large. Each size
             is named in text. The images are decoration. -->
        <ul
          class="icon-preview__sizes"
          aria-labelledby="icon-preview-nodes"
          data-testid="icon-preview-sizes">
          <li v-for="side in NODE_SIZES" :key="side">
            <span class="icon-preview__frame icon-preview__frame--node">
              <builder-icon v-if="icon.builtin" :name="icon.key" :size="side" />
              <builder-icon v-else name="image" :src="src" :size="side" />
            </span>
            <span>{{ side }} pixels</span>
          </li>
        </ul>
      </div>
    </div>

    <dl
      v-if="icon.builtin"
      class="icon-preview__details"
      data-testid="icon-preview-details">
      <div>
        <dt>Name</dt>
        <dd>{{ name }}</dd>
      </div>
      <div>
        <dt>Key</dt>
        <dd>{{ icon.key }}</dd>
      </div>
      <div>
        <dt>Source</dt>
        <dd>{{ BUILTIN_ICON_OWNER }}</dd>
      </div>
      <div>
        <dt>File</dt>
        <dd>
          PNG, {{ BUILTIN_ICON_FILE_SIZE }} × {{ BUILTIN_ICON_FILE_SIZE }}
          pixels
        </dd>
      </div>
    </dl>
    <dl v-else class="icon-preview__details" data-testid="icon-preview-details">
      <div>
        <dt>Name</dt>
        <dd>{{ icon.name }}</dd>
      </div>
      <div v-if="icon.aliases?.length">
        <dt>Other names</dt>
        <dd>{{ icon.aliases.join(', ') }}</dd>
      </div>
      <div>
        <dt>Source</dt>
        <dd>{{ iconOwnerText(icon) }}</dd>
      </div>
      <div>
        <dt>Size</dt>
        <dd>{{ sizeText }}, {{ bytesText }}</dd>
      </div>
    </dl>

    <p class="builder-dialog__message" role="status">
      <span
        v-if="status.text"
        :key="status.key"
        data-testid="icon-preview-status"
        >{{ status.text }}</span
      >
    </p>
    <p class="builder-dialog__message builder-dialog__error" role="alert">
      <span
        v-if="error.text"
        :key="error.key"
        data-testid="icon-preview-error"
        >{{ error.text }}</span
      >
    </p>

    <div class="builder-dialog__actions">
      <builder-action-button
        action="download"
        data-testid="icon-preview-download"
        :aria-label="`Download ${name}`"
        @click="$emit('download', icon)" />
      <button
        type="button"
        class="builder-button"
        data-testid="icon-preview-close"
        @click="$emit('close')">
        Close
      </button>
    </div>
  </builder-dialog>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderActionButton from '../BuilderActionButton.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';

  import {
    BUILTIN_ICON_FILE_SIZE,
    BUILTIN_ICON_OWNER,
  } from '@/builder/builtinIcons.js';
  import {
    iconDetails,
    iconOwnerText,
    iconSrc,
    isIconName,
  } from '@/builder/icons.js';

  // The sizes a node draws its icon at (iconSize: small, medium, large).
  const NODE_SIZES = [16, 24, 32];
  // The size a built-in icon is shown at first, as large as a stored icon
  // can be.
  const LARGE_SIZE = 96;

  const props = defineProps({
    // An icon as the library lists it: name, data, and owner, aliases,
    // width, height and bytes. Or a built-in icon: {builtin, key, label}.
    icon: { type: Object, required: true },
    // The status and error messages of the tab (useMessage), for example
    // the result of Download.
    status: { type: Object, default: () => ({ text: '', key: 0 }) },
    error: { type: Object, default: () => ({ text: '', key: 0 }) },
  });

  defineEmits(['close', 'download']);

  // The name the dialog gives the icon: a built-in icon's label.
  const name = computed(() =>
    props.icon.builtin ? props.icon.label : props.icon.name,
  );

  const src = computed(() => {
    if (props.icon.builtin) {
      return '';
    }

    const key = isIconName(props.icon.name) ? props.icon.name : 'icon';

    return iconSrc(key, { [key]: props.icon });
  });

  // The size the library states, or else the one its data holds.
  const size = computed(() => {
    if (props.icon.builtin) {
      return { width: 0, height: 0, bytes: 0 };
    }

    const { width, height, bytes } = props.icon;

    return width > 0 && height > 0 && bytes > 0
      ? { width, height, bytes }
      : iconDetails(props.icon.data) || { width: 0, height: 0, bytes: 0 };
  });

  const sizeText = computed(
    () => `${size.value.width} × ${size.value.height} pixels`,
  );
  const bytesText = computed(
    () => `${size.value.bytes.toLocaleString('en-US')} bytes`,
  );
</script>

<style scoped>
  .icon-preview {
    width: min(32rem, 92vw);
  }

  .icon-preview__body {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 1rem 1.5rem;
  }

  .icon-preview__stored {
    margin: 0;
  }

  /* The panel color around the icon, with a frame, in both themes. */
  .icon-preview__frame {
    display: grid;
    place-items: center;
    min-width: 7rem;
    min-height: 7rem;
    padding: 0.5rem;
    border: 1px solid var(--bx-border-strong);
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
  }

  .icon-preview__frame--node {
    min-width: 3rem;
    min-height: 3rem;
    padding: 0.25rem;
  }

  /* Each stored pixel is a sharp square, also on a screen that scales the
     page. The application's styles must not stretch it. */
  .icon-preview__pixels {
    max-width: none;
    height: auto;
    image-rendering: pixelated;
  }

  .icon-preview__caption {
    margin-top: 0.3rem;
    font-size: 0.85rem;
    color: var(--bx-text-muted);
  }

  .icon-preview__heading {
    margin: 0 0 0.4rem;
    font-size: 0.95rem;
    font-weight: 700;
  }

  .icon-preview__sizes {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .icon-preview__sizes li {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.85rem;
  }

  .icon-preview__details {
    display: grid;
    gap: 0.25rem;
    margin: 1rem 0;
  }

  .icon-preview__details > div {
    display: flex;
    flex-wrap: wrap;
    gap: 0 0.5rem;
  }

  .icon-preview__details dt {
    font-weight: 600;
  }

  .icon-preview__details dt::after {
    content: ':';
  }

  .icon-preview__details dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
</style>
