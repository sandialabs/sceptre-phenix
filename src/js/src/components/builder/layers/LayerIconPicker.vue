<!--
  The icon menu button of a layer or a layer set, as InspectorIconControl.vue. A
  choice is one `change` of {iconKey, icon}, with '' for a removed field.
-->
<template>
  <div class="builder-layer-icon">
    <builder-menu-button
      :id="id"
      class="builder-layer-icon__button"
      :items="items"
      :disabled="readOnly"
      menu-label="Icon"
      :testid="testid"
      :aria-label="`${label}: ${name}`"
      @select="onSelect">
      <builder-icon
        v-if="shown.key || shown.src"
        :name="shown.key || 'image'"
        :src="shown.src"
        :size="18" />
      <span v-else class="builder-layer-icon__none" aria-hidden="true">–</span>
      <span v-if="wide" class="builder-layer-icon__name">{{ name }}</span>
    </builder-menu-button>

    <icon-dialog v-if="open" @close="open = false" @use="use" />
  </div>
</template>

<script setup>
  import { computed, ref } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderMenuButton from '../BuilderMenuButton.vue';
  import IconDialog from '../dialogs/IconDialog.vue';
  import { useInspectorIcons } from '../inspector/control.js';

  import { isIconKey } from '@/builder/catalog.js';
  import { ICON_ORDER, iconLabel } from '@/builder/iconLabels.js';
  import { iconSrc, isIconName } from '@/builder/icons.js';

  // The id of the menu's last item, which opens the dialog.
  const CUSTOM = 'custom-icon';

  const props = defineProps({
    // The built-in icon key, and the custom icon's name. '' for none.
    iconKey: { type: String, default: '' },
    icon: { type: String, default: '' },
    // The button's name before the icon's name: "Icon of layer 3".
    label: { type: String, required: true },
    readOnly: { type: Boolean, default: false },
    // Show the icon's name in the button, not only its picture.
    wide: { type: Boolean, default: false },
    id: { type: String, default: undefined },
    testid: { type: String, default: 'layer-icon' },
  });

  const emit = defineEmits(['change']);

  const icons = useInspectorIcons();
  const open = ref(false);

  const custom = computed(() => (isIconName(props.icon) ? props.icon : ''));
  const entry = computed(() =>
    custom.value ? icons.entry(custom.value) : undefined,
  );
  const src = computed(() =>
    entry.value ? iconSrc(custom.value, { [custom.value]: entry.value }) : '',
  );
  const key = computed(() => (isIconKey(props.iconKey) ? props.iconKey : ''));

  // What the button draws: the custom icon when it resolves, else the
  // built-in icon.
  const shown = computed(() => ({
    key: key.value || (custom.value ? 'image' : ''),
    src: src.value,
  }));

  const name = computed(() => {
    if (custom.value) {
      return entry.value
        ? `${custom.value} (custom icon)`
        : `${custom.value} (custom icon not found)`;
    }

    return key.value ? iconLabel(key.value) : 'No icon';
  });

  const items = computed(() => [
    {
      id: 'none',
      label: 'No icon',
      checked: !key.value && !custom.value,
    },
    ...ICON_ORDER.filter((value) => isIconKey(value)).map((value) => ({
      id: value,
      key: value,
      label: iconLabel(value),
      icon: value,
      checked: !custom.value && value === key.value,
    })),
    {
      id: CUSTOM,
      label: 'Custom icon…',
      description: custom.value || undefined,
      icon: 'image',
      src: src.value,
      checked: Boolean(custom.value),
      separator: true,
    },
  ]);

  function onSelect(item) {
    if (item.id === CUSTOM) {
      open.value = true;

      return;
    }

    const next = { iconKey: item.key || '', icon: '' };

    if (next.iconKey !== key.value || custom.value) {
      emit('change', next);
    }
  }

  // The dialog's choice: the custom icon, in place of a built-in icon.
  function use(icon) {
    open.value = false;

    if (icon.name !== custom.value || key.value) {
      emit('change', { iconKey: '', icon: icon.name });
    }
  }
</script>

<style scoped>
  .builder-layer-icon {
    display: flex;
    min-width: 0;
  }

  .builder-layer-icon :deep(.builder-menu) {
    display: flex;
    min-width: 0;
  }

  /* As tall as the text fields beside it. */
  .builder-layer-icon :deep(.builder-layer-icon__button) {
    min-height: 2.25rem;
    min-width: 2.75rem;
    padding: 0.2rem 0.4rem;
    border-color: var(--bx-field-border);
    background: var(--bx-surface);
    color: var(--bx-text);
  }

  .builder-layer-icon :deep(.builder-layer-icon__button[aria-disabled='true']) {
    border-style: dashed;
    background: var(--bx-bg-alt);
    opacity: 1;
  }

  .builder-layer-icon__none {
    display: inline-block;
    width: 18px;
    text-align: center;
    color: var(--bx-text-muted);
  }

  .builder-layer-icon__name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.85rem;
  }

  /* The built-in icons are many: the menu scrolls. */
  .builder-layer-icon :deep(.builder-menu__list) {
    max-height: min(20rem, 60vh);
    overflow-y: auto;
    box-shadow:
      0 0 0 1px var(--bx-surface),
      var(--bx-shadow);
  }
</style>
