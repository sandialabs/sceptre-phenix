<!--
  The layer select of a device or a switch. A choice is one undo step at once,
  without Apply (see chooseLayer in BuilderInspector.vue).
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
      <builder-icon name="layer-group" :size="14" />
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
   * The layer select's choices: the layers and the node's own layer, highest ID
   * first.
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

  const hint = 'The By layer layout puts higher layer IDs above lower ones.';
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
