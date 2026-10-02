<!--
  The custom icon of a device or a group: an image of the user's own, drawn
  in place of the built-in icon. The field holds the icon's id; the diagram
  carries the image itself (see icons.js).

  With no icon the field says None and offers Choose…; with one it shows the
  icon, its name, Change… and Remove. Choose… and Change… open the Custom
  icons dialog (IconDialog), where an icon is uploaded or picked from the
  diagram's or from the user's library. The control mounts the dialog
  itself, so it works in any form the Inspector's renderers are used in.

  The icons come from what the form's host provides (see INSPECTOR_ICONS in
  control.js). An icon picked in the dialog is made known to the host first,
  and the field then takes its id, so the edit that applies the field finds
  the image to copy into the document.

  A locked or read-only form disables the buttons.
-->
<template>
  <div v-if="control.visible" class="inspector-icon-control">
    <inspector-field
      :control="control"
      :ids="ids"
      :error-text="errorText"
      :warnings="warnings"
      :changed="changed">
      <div class="inspector-icon">
        <builder-icon v-if="value" name="image" :src="src" :size="20" />
        <span class="inspector-icon__name" data-testid="inspector-icon-name">{{
          name
        }}</span>
        <!-- One button for both, so it keeps focus when an icon is chosen
             and takes it back from the dialog. -->
        <button
          :id="ids.input"
          ref="chooser"
          type="button"
          class="builder-button"
          data-testid="inspector-icon-choose"
          :aria-label="value ? 'Change custom icon' : 'Choose custom icon'"
          aria-haspopup="dialog"
          :aria-describedby="inputAttrs['aria-describedby']"
          :disabled="disabled"
          @click="open = true">
          {{ value ? 'Change…' : 'Choose…' }}
        </button>
        <button
          v-if="value"
          type="button"
          class="builder-button"
          data-testid="inspector-icon-remove"
          aria-label="Remove custom icon"
          :disabled="disabled"
          @click="remove">
          Remove
        </button>
      </div>
    </inspector-field>

    <icon-dialog v-if="open" @close="open = false" @use="use" />
  </div>
</template>

<script setup>
  import { computed, nextTick, ref } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import IconDialog from '../dialogs/IconDialog.vue';
  import InspectorField from './InspectorField.vue';
  import { useInspectorControl, useInspectorIcons } from './control.js';

  import { ICON_ID, iconSrc } from '@/builder/icons.js';

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);
  const { control, ids, errorText, warnings, inputAttrs, locked, changed } =
    useInspectorControl(input);
  const icons = useInspectorIcons();

  const chooser = ref();
  const open = ref(false);

  // The icon the field names, or '' for none.
  const value = computed(() => {
    const id = control.value.data;

    return typeof id === 'string' && ICON_ID.test(id) ? id : '';
  });
  const entry = computed(() =>
    value.value ? icons.entry(value.value) : undefined,
  );
  const src = computed(() =>
    entry.value ? iconSrc(value.value, { [value.value]: entry.value }) : '',
  );

  const name = computed(() => {
    if (!value.value) {
      return 'None';
    }

    if (!entry.value) {
      return 'Unknown icon';
    }

    return entry.value.name || 'Unnamed icon';
  });

  const disabled = computed(() => locked.value || !control.value.enabled);

  // The dialog's choice: the icon is made known, then the field names it.
  function use(icon) {
    open.value = false;
    icons.shelve(icon.id, { name: icon.name, data: icon.data });

    if (icon.id !== control.value.data) {
      input.handleChange(control.value.path, icon.id);
    }
  }

  // Remove goes with the icon, so focus moves to the button that stays.
  async function remove() {
    input.handleChange(control.value.path, undefined);
    await nextTick();
    chooser.value?.focus();
  }
</script>

<style scoped>
  .inspector-icon {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
  }

  /* A long name is cut, with the buttons still beside it. */
  .inspector-icon__name {
    flex: 1 1 4rem;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.85rem;
  }

  .inspector-icon .builder-button {
    flex: none;
  }
</style>
