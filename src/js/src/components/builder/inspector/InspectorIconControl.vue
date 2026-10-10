<!--
  The custom icon of a device or a group: an image of the server's icon
  library, drawn in place of the built-in icon. The field holds the icon's
  name, which the document's copy of the icon or the icon library resolves
  (see iconSrc in icons.js). With neither, the node draws its built-in icon.

  With no icon, the field says None and offers Choose…. With an icon, it
  shows the icon, its name, Change… and Remove. Choose… and Change… open the
  Custom icons dialog (IconDialog), where the user uploads an icon or picks
  one from the server's or from the diagram's copies. The control mounts
  the dialog itself, so it works in any form the Inspector's renderers are
  used in.

  The icons come from what the form's host provides (see INSPECTOR_ICONS in
  control.js).

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

  import { iconSrc, isIconName } from '@/builder/icons.js';

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);
  const { control, ids, errorText, warnings, inputAttrs, locked, changed } =
    useInspectorControl(input);
  const icons = useInspectorIcons();

  const chooser = ref();
  const open = ref(false);

  // The icon the field names, or '' for none.
  const value = computed(() => {
    const name = control.value.data;

    return isIconName(name) ? name : '';
  });
  const entry = computed(() =>
    value.value ? icons.entry(value.value) : undefined,
  );
  const src = computed(() =>
    entry.value ? iconSrc(value.value, { [value.value]: entry.value }) : '',
  );

  // The name the field holds, and what a name nothing resolves means: the
  // node shows its built-in icon.
  const name = computed(() => {
    if (!value.value) {
      return 'None';
    }

    return entry.value
      ? value.value
      : `${value.value} (not found: the built-in icon is shown)`;
  });

  const disabled = computed(() => locked.value || !control.value.enabled);

  // The dialog's choice: the field names the icon.
  function use(icon) {
    open.value = false;

    if (icon.name !== control.value.data) {
      input.handleChange(control.value.path, icon.name);
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
