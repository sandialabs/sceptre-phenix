<!--
  The Icon field, for `iconKey` and `icon` both. It is a menu button: a native
  select sends no change for the chosen option, so it cannot reopen the custom
  icon dialog.
-->
<template>
  <div v-if="control.visible" class="inspector-icon-control">
    <inspector-field
      :control="control"
      :ids="ids"
      :error-text="errorText"
      :warnings="warnings"
      :note="fieldNote"
      :changed="changed || iconChanged">
      <!-- The custom icon's path, for Go to and the error summary. -->
      <div :data-path="iconPath" class="inspector-icon">
        <builder-menu-button
          :id="ids.input"
          class="inspector-icon__button"
          :items="items"
          :disabled="disabled"
          menu-label="Icon"
          testid="inspector-icon"
          :aria-labelledby="`${ids.label} ${ids.value}`"
          :aria-describedby="inputAttrs['aria-describedby']"
          :aria-invalid="inputAttrs['aria-invalid']"
          @select="onSelect">
          <builder-icon :name="shownKey" :src="src" :size="20" />
          <span :id="ids.value" class="inspector-icon__value"
            ><span data-testid="inspector-icon-name">{{ name }}</span
            ><span
              v-if="note"
              class="inspector-icon__note"
              data-testid="inspector-icon-note"
              >{{ ` ${note}` }}</span
            ></span
          >
        </builder-menu-button>
      </div>
    </inspector-field>

    <icon-dialog v-if="open" @close="open = false" @use="use" />
  </div>
</template>

<script setup>
  import { computed, inject, ref } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderMenuButton from '../BuilderMenuButton.vue';
  import IconDialog from '../dialogs/IconDialog.vue';
  import InspectorField from './InspectorField.vue';
  import {
    useFieldChanged,
    useInspectorControl,
    useInspectorIcons,
    useUnsetValue,
  } from './control.js';

  import { isIconKey } from '@/builder/catalog.js';
  import { ICON_ORDER, iconLabel } from '@/builder/iconLabels.js';
  import { iconSrc, isIconName } from '@/builder/icons.js';

  // The id of the menu's last item, which opens the dialog.
  const CUSTOM = 'custom-icon';

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);
  const jsonforms = inject('jsonforms', null);
  const icons = useInspectorIcons();
  const unset = useUnsetValue(input.control);

  // The keys the menu offers: those of the field's schema that the registry
  // knows, in the order of ICON_ORDER.
  // Default first, where the schema offers it and the node then draws an
  // icon (see fieldDefault): an icon drawing has no Default.
  const offered = computed(() => {
    const keys = input.control.value.schema?.enum ?? [];
    const known = (key) => keys.includes(key) && isIconKey(key);

    return [
      ...(keys.includes('') && unset.value ? [''] : []),
      ...ICON_ORDER.filter(known),
      ...keys.filter((key) => !ICON_ORDER.includes(key) && known(key)),
    ];
  });

  const key = computed(() => String(input.control.value.data ?? ''));
  // A key that the menu does not offer, as an uploaded topology can hold.
  const outside = computed(() =>
    key.value && !offered.value.includes(key.value) ? key.value : '',
  );

  const {
    control,
    ids: fieldIds,
    errorText,
    warnings,
    inputAttrs,
    locked,
    changed,
    note: fieldNote,
  } = useInspectorControl(input, undefined, {
    moreWarnings: computed(() =>
      outside.value ? [`"${outside.value}" is not one of the choices.`] : [],
    ),
  });

  const ids = computed(() => ({
    ...fieldIds.value,
    label: `${fieldIds.value.input}-label`,
    value: `${fieldIds.value.input}-value`,
  }));

  // The custom icon's data path, beside the key's.
  const iconPath = computed(() =>
    control.value.path.replace(/iconKey$/, 'icon'),
  );
  const iconChanged = useFieldChanged(
    computed(() => ({ path: iconPath.value })),
  );

  // The custom icon the node names, or '' for none.
  const custom = computed(() => {
    const name = String(iconPath.value)
      .split('.')
      .reduce((data, part) => data?.[part], jsonforms?.core?.data);

    return isIconName(name) ? name : '';
  });
  const entry = computed(() =>
    custom.value ? icons.entry(custom.value) : undefined,
  );
  const src = computed(() =>
    entry.value ? iconSrc(custom.value, { [custom.value]: entry.value }) : '',
  );

  // The built-in icon a key draws: Default draws the node's default icon.
  const fallbackKey = computed(() => String(unset.value?.value || 'server'));
  const drawn = (value) => value || fallbackKey.value;

  function keyLabel(value) {
    if (value) {
      return iconLabel(value);
    }

    return unset.value
      ? `Default (${iconLabel(fallbackKey.value)})`
      : 'Default';
  }

  // The built-in icon drawn while no custom icon resolves.
  const shownKey = computed(() => drawn(key.value));

  // The name of the icon the node draws, and what it is when it is not a
  // built-in icon of the menu.
  const name = computed(
    () => custom.value || outside.value || keyLabel(key.value),
  );
  const note = computed(() => {
    if (!custom.value) {
      return outside.value ? '(not one of the choices)' : '';
    }

    return entry.value
      ? '(custom icon)'
      : '(custom icon not found: the node shows its built-in icon)';
  });

  const items = computed(() => [
    ...offered.value.map((value) => ({
      id: value || 'default',
      key: value,
      label: keyLabel(value),
      icon: drawn(value),
      checked: !custom.value && value === key.value,
    })),
    ...(outside.value
      ? [
          {
            id: 'outside',
            key: outside.value,
            label: `${outside.value} (not one of the choices)`,
            icon: drawn(''),
            checked: !custom.value,
          },
        ]
      : []),
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

  const disabled = computed(() => locked.value || !control.value.enabled);
  const open = ref(false);

  // A built-in icon takes the place of the custom icon. Both change in the
  // same task, so the form sends one change for them.
  function onSelect(item) {
    if (item.id === CUSTOM) {
      open.value = true;

      return;
    }

    if (custom.value) {
      input.handleChange(iconPath.value, undefined);
    }

    if (item.key !== key.value) {
      input.handleChange(control.value.path, item.key);
    }
  }

  // The dialog's choice: the node names the icon.
  function use(icon) {
    open.value = false;

    if (icon.name !== custom.value) {
      input.handleChange(iconPath.value, icon.name);
    }
  }
</script>

<style scoped>
  .inspector-icon {
    display: flex;
    min-width: 0;
  }

  .inspector-icon :deep(.builder-menu) {
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
  }

  /* A field, not a toolbar button: as wide as the other fields, with the
     icon and its name at the start and the arrow at the end. It has their
     box too (Bulma's .input and .select, and the Inspector's padding), so
     it lines up with the fields beside it in a row of columns. */
  .inspector-icon :deep(.inspector-icon__button) {
    flex: 1 1 auto;
    justify-content: flex-start;
    min-width: 0;
    min-height: 2.5em;
    padding: 0.3rem 0.4rem;
    font-size: 1rem;
    line-height: 1.5;
    border-color: var(--bx-field-border);
    background: var(--bx-surface);
    color: var(--bx-text);
    text-align: start;
  }

  .inspector-icon :deep(.inspector-icon__button[aria-disabled='true']) {
    border-style: dashed;
    background: var(--bx-bg-alt);
    color: var(--bx-text);
    opacity: 1;
  }

  /* A long name is cut, with the arrow still beside it. */
  .inspector-icon__value {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.85rem;
  }

  .inspector-icon__note {
    color: var(--bx-text-muted);
  }

  /* The built-in icons are many: the menu scrolls. The menu's edges and
     the labels' underlines under it can round to different pixels, so a
     ring in the menu's own color covers the pixel between them. */
  .inspector-icon :deep(.builder-menu__list) {
    max-height: min(20rem, 60vh);
    overflow-y: auto;
    box-shadow:
      0 0 0 1px var(--bx-surface),
      var(--bx-shadow);
  }
</style>
