<!--
  The layer of a device or a switch: a select of the diagram's layers, with
  a hint. It acts at once, without Apply: a choice is one undo step (see
  chooseLayer in BuilderInspector.vue). The By layer layout reads it, and
  publishing never writes it to a config. A locked node or a read-only
  draft shows a read-only text field in place of the select, as a locked
  field of the form does (see InspectorEnumControl.vue).

  The choices are None, then the layers the diagram offers (its own, or
  the placeholder layers, see effectiveLayers in model.js), the highest ID
  first, as the By layer layout stacks them. A node can name a layer ID
  that the diagram does not define, such as after its layer was removed.
  That ID is a choice too, named "Layer N".

  A device that no one put at a layer is at the layer its fields match,
  with the node matchers of the diagram's layers (`matched`, see
  nodeLayerState in model.js). The first choice then names that layer and
  the field that matched, "Operations (matched general.hostname)", in
  place of None. A layer chosen for the device goes before the matched
  layer. Use matched layer then removes the choice, so that the device is
  at the matched layer again, and focus goes to the select.

  The icon and the color of the layer the node is at show before the
  select (see LayerName.vue): the options of a native select cannot show
  them.

  The select and the read-only text field have the classes of the form's
  fields (select, input, see InspectorEnumControl.vue), so they are as tall
  as the other fields.

  It is drawn one of two ways (`as`):

  - 'section': a section of its own with a heading, as the canvas
    Inspector shows it, above the connection points
  - 'field': one field of the form, with a label, as the template editor
    and the Node Editor show it after the node's look fields (see
    InspectorLayerRenderer.vue).

  With `editable`, Edit layers… shows after the select. It opens the
  layers editor (dialogs/LayersDialog.vue), where the diagram's layers
  change. The template editor has no Edit layers…: its layers are a copy,
  which the layers editor does not change.
-->
<template>
  <div
    class="builder-inspector__layer"
    :class="{ control: as === 'field' }"
    data-testid="inspector-layer">
    <h3 v-if="as === 'section'">
      <label :for="ids.select">Layer</label>
    </h3>
    <label v-else :for="ids.select">Layer</label>
    <p v-if="as === 'section'" :id="ids.hint" class="builder-inspector__hint">
      {{ hint }}
    </p>
    <div class="builder-inspector__layer-row">
      <layer-name
        v-if="shownLayer"
        bare
        :layer="shownLayer"
        :icons="icons"
        data-testid="inspector-layer-look" />
      <input
        v-if="locked"
        :id="ids.select"
        type="text"
        class="input"
        readonly
        aria-readonly="true"
        autocomplete="off"
        :aria-describedby="ids.hint"
        data-testid="inspector-layer-select"
        :value="current" />
      <select
        v-else
        :id="ids.select"
        class="select"
        :aria-describedby="ids.hint"
        data-testid="inspector-layer-select"
        :value="layer"
        @change="$emit('choose', $event.target.value)">
        <option value="">{{ noneTitle }}</option>
        <option v-for="choice in choices" :key="choice.id" :value="choice.id">
          {{ choice.title }}
        </option>
      </select>
    </div>
    <p v-if="as === 'field'" :id="ids.hint" class="builder-inspector__hint">
      {{ hint }}
    </p>
    <template v-if="overridden">
      <p
        :id="`${ids.select}-matched`"
        class="builder-inspector__hint"
        data-testid="inspector-layer-matched">
        Its fields match {{ matchedTitle }} ({{ matched.path }}). The layer that
        you chose goes first.
      </p>
      <button
        v-if="!locked"
        type="button"
        class="builder-button builder-inspector__layer-edit"
        :aria-describedby="`${ids.select}-matched`"
        data-testid="inspector-layer-use-matched"
        @click="useMatched">
        Use matched layer
      </button>
    </template>
    <button
      v-if="editable && !locked"
      type="button"
      class="builder-button builder-inspector__layer-edit"
      aria-haspopup="dialog"
      data-testid="inspector-layer-edit"
      @click="$emit('edit-layers')">
      <builder-icon name="settings" :size="14" />
      Edit layers…
    </button>
  </div>
</template>

<script>
  import { layerTitles, placeholderLabel } from '@/builder/model.js';

  // How a layer is named among the choices: its label, "Operations".
  function choiceTitle(layers, id) {
    return layerChoices(layers, id).find((choice) => choice.id === id).title;
  }

  // What the layer says of a node at no layer.
  const LAYER_NONE = 'None';

  /**
   * The choices of the layer select: the layers given, and the node's own
   * layer when they do not have it, the highest ID first, each named by
   * its label (see layerTitles).
   *
   * @param {{id: number, label: string}[]} layers the layers the diagram
   *   offers
   * @param {number|string} own the node's layer ID, '' for none
   * @returns {{id: number, title: string}[]}
   */
  export function layerChoices(layers, own) {
    const listed = layers.map(({ id, label }) => ({ id, label }));

    if (typeof own === 'number' && !listed.some((layer) => layer.id === own)) {
      listed.push({ id: own, label: placeholderLabel(own) });
    }

    const titles = layerTitles(listed);

    return listed
      .sort((a, b) => b.id - a.id)
      .map(({ id }) => ({ id, title: titles.get(id) }));
  }
</script>

<script setup>
  import { computed, nextTick } from 'vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import LayerName from '../layers/LayerName.vue';

  import { useInspectorIconLibrary } from './control.js';

  import { hasLayerLook } from '@/builder/layerLook.js';

  const props = defineProps({
    as: {
      type: String,
      default: 'section',
      validator: (value) => ['section', 'field'].includes(value),
    },
    // 'device' or 'switch'.
    kind: { type: String, required: true },
    // The layer ID chosen for the node, '' for none.
    layer: { type: [String, Number], default: '' },
    // The layer its fields match, {id, path}, or null.
    matched: { type: Object, default: null },
    // The layers the diagram offers, each {id, label, iconKey?, icon?,
    // color?}.
    layers: { type: Array, default: () => [] },
    // The custom icons the diagram carries, for the layers' icons.
    icons: { type: Object, default: null },
    locked: { type: Boolean, default: false },
    // The ids of the select and of its hint.
    ids: { type: Object, required: true },
    // Show Edit layers… (see above).
    editable: { type: Boolean, default: false },
  });

  // choose: a layer was selected. edit-layers: Edit layers… was selected.
  // use-matched: Use matched layer was selected.
  const emit = defineEmits(['choose', 'edit-layers', 'use-matched']);

  const library = useInspectorIconLibrary();

  const choices = computed(() => layerChoices(props.layers, props.layer));

  const matchedTitle = computed(() =>
    props.matched ? choiceTitle(props.layers, props.matched.id) : '',
  );

  // The first choice: the matched layer, else None.
  const noneTitle = computed(() =>
    props.matched
      ? `${matchedTitle.value} (matched ${props.matched.path})`
      : LAYER_NONE,
  );

  // A layer was chosen for the node, and its fields match a layer too.
  const overridden = computed(
    () => typeof props.layer === 'number' && Boolean(props.matched),
  );

  // The read-only text: the title of the node's layer, or the first
  // choice.
  const current = computed(
    () =>
      choices.value.find((choice) => choice.id === props.layer)?.title ||
      noneTitle.value,
  );

  // The layer the node is at, when it has an icon or a color to show.
  const shownLayer = computed(() => {
    const id =
      typeof props.layer === 'number' ? props.layer : props.matched?.id;
    const layer = props.layers.find((entry) => entry.id === id);

    return hasLayerLook(layer, props.icons, library) ? layer : null;
  });

  // The button goes with the choice it removes, so focus goes to the
  // select.
  async function useMatched() {
    emit('use-matched');
    await nextTick();
    document.getElementById(props.ids.select)?.focus();
  }

  const hint = computed(
    () =>
      `The layer of this ${props.kind === 'switch' ? 'network' : 'device'}. The By layer layout puts higher layer IDs above lower ones.`,
  );
</script>

<style scoped>
  /* As the other part headings of the Inspector: a little larger than the
     fields, in the text color of either theme (see BuilderInspector.vue). */
  .builder-inspector__layer h3 {
    font-weight: 700;
    font-size: 0.95rem;
    color: var(--bx-text);
    margin: 0.75rem 0 0.25rem;
  }

  .builder-inspector__layer-edit {
    margin-top: 0.35rem;
  }

  .builder-inspector__layer.control .builder-inspector__hint {
    margin: 0.2rem 0 0;
  }

  /* The layer's icon and swatch before the select, which takes the rest of
     the row. */
  .builder-inspector__layer-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .builder-inspector__layer-row > select,
  .builder-inspector__layer-row > input {
    flex: 1 1 auto;
    min-width: 0;
  }
</style>
