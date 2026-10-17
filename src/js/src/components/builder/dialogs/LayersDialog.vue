<!--
  The layers editor. It edits one list of layers, in one of two modes:

  - 'diagram': the layers of the open diagram, which the diagram keeps a
    copy of. It opens from Edit layers… in the Inspector (the Diagram
    section and the Layer field), in the Node Editor, at the bottom of the
    Settings dialog and in the command palette. Each change is one undo
    step of the diagram (the store's addLayer, relabelLayer, updateLayer,
    renumberLayer and removeLayer), and Done closes it.
  - 'library': one layer set of the server's library (setId), or a new one
    (no setId), from the Layers tab of the Drafts page
    (BuilderLayerSets.vue). It edits a copy, and Save writes it to the
    server (the store's saveLayerSet or createLayerSet), which gives the
    set its next version. Cancel drops the copy, and asks first when the
    copy changed. A built-in set is read only: Duplicate on the tab makes a
    copy that can change.

  In both modes the layers are LayerRows.vue, and the set's options are the
  rule for a device whose fields match the matchers of more than one layer
  (lowest or highest layer ID), and whether node outlines take the color
  of their layer.

  In diagram mode the Layer set part says how the diagram's copy stands to
  the set it came from (see layerSetLink in model.js): not linked, linked
  to a version, changed in the diagram, a newer version in the library, or
  a set that the library does not have. Update from layer set copies the
  newer version. Save to layer set writes the diagram's changed copy to its
  set, and Save as new set… adds it to the library as a new set. Both link
  the diagram to the version they wrote. Unlink removes the link and keeps
  the layers. Use a layer set with Apply replaces the diagram's layers and
  options with a copy of a set of the library and links them. Nodes keep
  the layers chosen for them. Each of these is one undo step.

  The page's live region waits while a modal dialog is open, so the dialog
  speaks through a status region of its own. A failure shows in its alert.
-->
<template>
  <builder-dialog
    :title="title"
    title-id="layers-dialog-title"
    class="builder-layers-dialog"
    data-testid="layers-dialog"
    @close="requestClose">
    <!-- The fields scroll, and the buttons stay in view. tabindex: Firefox
         makes a box that scrolls a Tab stop of its own. -->
    <div class="builder-layers-dialog__body" tabindex="-1">
      <template v-if="diagram">
        <p class="builder-hint builder-layers-dialog__intro">
          The layers of this diagram. The diagram keeps its own copy of them. A
          device or a switch can be at a layer. The By layer layout puts the
          highest layer ID at the top.
        </p>
        <p v-if="readOnly" class="builder-hint" data-testid="layers-read-only">
          This draft is read only. You cannot change its layers.
        </p>

        <h3 class="builder-layers-dialog__heading">Layer set</h3>
        <p
          id="layers-link"
          class="builder-layers-dialog__link"
          data-testid="layers-link">
          {{ linkText }}
        </p>
        <div
          v-if="!readOnly && (link.status !== 'unknown' || failed)"
          class="builder-layers-dialog__actions">
          <button
            v-if="failed"
            type="button"
            class="builder-button"
            data-testid="layers-retry"
            @click="retry">
            Retry
          </button>
          <button
            v-if="link.status === 'update'"
            type="button"
            class="builder-button builder-button--primary"
            aria-describedby="layers-link"
            data-testid="layers-update"
            @click="updateFromSet">
            <builder-icon name="refresh" :size="14" />
            Update from layer set
          </button>
          <button
            v-if="canSaveToSet"
            type="button"
            class="builder-button"
            aria-describedby="layers-link"
            :aria-disabled="busy || undefined"
            data-testid="layers-save-to-set"
            @click="saveToSet">
            <builder-icon name="save" :size="14" />
            Save to layer set
          </button>
          <button
            type="button"
            class="builder-button"
            :aria-expanded="naming ? 'true' : 'false'"
            aria-controls="layers-new-set"
            data-testid="layers-save-new"
            @click="toggleNaming">
            Save as new set…
          </button>
          <button
            v-if="link.source"
            type="button"
            class="builder-button"
            aria-describedby="layers-link"
            data-testid="layers-unlink"
            @click="unlink">
            Unlink
          </button>
        </div>

        <form
          v-if="naming"
          id="layers-new-set"
          class="builder-panel builder-layers-dialog__new"
          data-testid="layers-new-set"
          novalidate
          @submit.prevent="saveAsNew">
          <div class="builder-field">
            <label for="layers-new-name">Name of the new set</label>
            <input
              id="layers-new-name"
              ref="newName"
              v-model="newSet.name"
              type="text"
              autocomplete="off"
              required
              :aria-invalid="fieldError.invalid('name')"
              :aria-describedby="fieldError.describedBy('name')"
              data-testid="layers-new-name" />
          </div>
          <div class="builder-field">
            <label for="layers-new-description">Description</label>
            <input
              id="layers-new-description"
              v-model="newSet.description"
              type="text"
              autocomplete="off"
              :aria-invalid="fieldError.invalid('description')"
              :aria-describedby="fieldError.describedBy('description')" />
          </div>
          <div class="builder-layers-dialog__actions">
            <button
              type="submit"
              class="builder-button builder-button--primary"
              :aria-disabled="busy || undefined"
              data-testid="layers-new-save">
              Save layer set
            </button>
            <button
              type="button"
              class="builder-button"
              data-testid="layers-new-cancel"
              @click="toggleNaming">
              Cancel
            </button>
          </div>
        </form>

        <div v-if="!readOnly" class="builder-field builder-layers-dialog__use">
          <label for="layers-choose">Use a layer set</label>
          <div class="builder-layers-dialog__row">
            <select
              id="layers-choose"
              v-model="chosen"
              aria-describedby="layers-choose-hint"
              :disabled="!sets.length || undefined"
              data-testid="layers-choose">
              <option v-for="set in sets" :key="set.id" :value="set.id">
                {{ setTitle(set) }}
              </option>
            </select>
            <button
              type="button"
              class="builder-button"
              :aria-disabled="!chosen || undefined"
              aria-describedby="layers-choose-hint"
              data-testid="layers-apply"
              @click="apply">
              Apply
            </button>
          </div>
          <p id="layers-choose-hint" class="builder-hint">
            Apply replaces the layers of this diagram with a copy of the set,
            and links the diagram to the set. Devices and switches keep the
            layers that you chose for them. Undo puts back the layers you had.
          </p>
        </div>
      </template>

      <template v-else>
        <p v-if="builtin" class="builder-hint" data-testid="layers-builtin">
          This layer set is part of phenix. You cannot change it. Select
          Duplicate on the Layers tab to make a copy that you can change.
        </p>
        <div class="builder-field">
          <label for="layers-set-name">Name</label>
          <input
            id="layers-set-name"
            v-model="local.name"
            type="text"
            autocomplete="off"
            required
            :readonly="viewOnly"
            :aria-invalid="fieldError.invalid('name')"
            :aria-describedby="fieldError.describedBy('name')"
            data-testid="layers-set-name" />
        </div>
        <div class="builder-field">
          <label for="layers-set-description">Description</label>
          <input
            id="layers-set-description"
            v-model="local.description"
            type="text"
            autocomplete="off"
            :readonly="viewOnly"
            :aria-invalid="fieldError.invalid('description')"
            :aria-describedby="fieldError.describedBy('description')"
            data-testid="layers-set-description" />
        </div>
        <div class="builder-field">
          <span class="builder-layers-dialog__label">Icon</span>
          <layer-icon-picker
            id="layers-set-icon"
            :icon-key="local.iconKey"
            :icon="local.icon"
            label="Icon of the layer set"
            wide
            :read-only="viewOnly"
            testid="layers-set-icon"
            @change="(icon) => Object.assign(local, icon)" />
        </div>
      </template>

      <h3 class="builder-layers-dialog__heading">Options</h3>
      <div class="builder-field">
        <label for="layers-conflict"
          >When a device matches more than one layer</label
        >
        <select
          id="layers-conflict"
          :value="options.matchConflict"
          :disabled="locked || undefined"
          aria-describedby="layers-conflict-hint"
          data-testid="layers-conflict"
          @change="setOption({ matchConflict: $event.target.value })">
          <option :value="MATCH_CONFLICT_LOWEST">Lowest layer ID wins</option>
          <option :value="MATCH_CONFLICT_HIGHEST">Highest layer ID wins</option>
        </select>
        <p id="layers-conflict-hint" class="builder-hint">
          The node matchers of more than one layer can match the fields of a
          device. This rule chooses the layer of the device.
        </p>
      </div>
      <div class="builder-field">
        <builder-switch
          :model-value="options.layerOutlines"
          :aria-disabled="locked || undefined"
          aria-describedby="layers-outlines-hint"
          data-testid="layers-outlines"
          @update:model-value="locked || setOption({ layerOutlines: $event })">
          Color node outlines with the layer color
        </builder-switch>
        <p id="layers-outlines-hint" class="builder-hint">
          A device or a switch at a layer that has a color gets an outline in
          that color. An outline color of the node itself goes first.
        </p>
      </div>

      <h3 class="builder-layers-dialog__heading">Layers</h3>
      <p
        v-if="diagram && placeholders"
        class="builder-hint"
        data-testid="layers-placeholders">
        This diagram has no layers of its own. It has
        {{ PLACEHOLDER_LAYERS }} placeholder layers. Your first change saves
        them in the diagram.
      </p>
      <layer-rows
        ref="rowsEl"
        :layers="layers"
        :ops="diagram ? diagramOps : libraryOps"
        :read-only="locked"
        :counts="diagram ? counts : null"
        @status="(text) => status.set(text)" />
      <p v-if="unlisted" class="builder-hint" data-testid="layers-unlisted">
        {{ unlisted }}
      </p>
    </div>

    <p
      class="builder-dialog__message builder-dialog__error"
      role="alert"
      data-testid="layers-error">
      <span v-if="error.text" :key="error.key">{{ error.text }}</span>
    </p>

    <div class="builder-dialog__actions builder-layers-dialog__footer">
      <template v-if="diagram || viewOnly">
        <button
          type="button"
          class="builder-button builder-button--primary"
          data-testid="layers-done"
          @click="$emit('close')">
          {{ diagram ? 'Done' : 'Close' }}
        </button>
      </template>
      <template v-else>
        <button
          type="button"
          class="builder-button"
          data-testid="layers-cancel"
          @click="requestClose">
          Cancel
        </button>
        <button
          type="button"
          class="builder-button builder-button--primary"
          :aria-disabled="busy || undefined"
          data-testid="layers-save"
          @click="save">
          Save
        </button>
      </template>
    </div>

    <p
      class="builder-visually-hidden"
      role="status"
      data-testid="layers-status">
      <span v-if="status.text" :key="status.key">{{ status.text }}</span>
    </p>
  </builder-dialog>

  <builder-confirm
    v-if="confirmingClose"
    id="layers-discard"
    title="Close without saving?"
    message="You changed this layer set. Close the editor without saving the changes?"
    confirm-label="Close without saving"
    cancel-label="Keep editing"
    @cancel="confirmingClose = false"
    @confirm="$emit('close')" />
</template>

<script setup>
  import {
    computed,
    nextTick,
    onMounted,
    provide,
    reactive,
    ref,
    watch,
  } from 'vue';

  import BuilderConfirm from '../BuilderConfirm.vue';
  import BuilderDialog from '../BuilderDialog.vue';
  import BuilderIcon from '../BuilderIcon.vue';
  import BuilderSwitch from '../BuilderSwitch.vue';
  import {
    INSPECTOR_ICON_LIBRARY,
    INSPECTOR_ICONS,
  } from '../inspector/control.js';
  import LayerIconPicker from '../layers/LayerIconPicker.vue';
  import LayerRows from '../layers/LayerRows.vue';
  import { useFieldError, useMessage } from './message.js';

  import { focusLost } from '@/builder/commands.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { resolveIcon } from '@/builder/icons.js';
  import {
    layerSetContent,
    uniqueLayerSetName,
  } from '@/builder/layerSetFile.js';
  import {
    effectiveLayerList,
    layerCounts,
    layerName,
    layerSetDescriptionProblem,
    layerSetNameProblem,
    layerSetOptions,
    layersProblem,
    MATCH_CONFLICT_HIGHEST,
    MATCH_CONFLICT_LOWEST,
    PLACEHOLDER_LAYERS,
    placeholderLabel,
    withLayerAdded,
    withLayerRemoved,
    withLayerRenumbered,
    withLayerUpdated,
  } from '@/builder/model.js';
  import { useBuilderStore } from '@/builder/store.js';

  const props = defineProps({
    mode: {
      type: String,
      default: 'diagram',
      validator: (value) => ['diagram', 'library'].includes(value),
    },
    // In library mode: the ID of the set to edit, or '' for a new set.
    setId: { type: String, default: '' },
  });

  // saved: a set of the library was saved, {set, created}.
  const emit = defineEmits(['close', 'saved']);

  const store = useBuilderStore();
  const status = useMessage();
  const fieldError = useFieldError('layers-error', {
    name: props.mode === 'diagram' ? 'layers-new-name' : 'layers-set-name',
    description:
      props.mode === 'diagram'
        ? 'layers-new-description'
        : 'layers-set-description',
  });
  const error = fieldError.error;

  const diagram = props.mode === 'diagram';
  const rowsEl = ref(null);
  const newName = ref(null);
  // A change of the library in progress: Save, Save to layer set or Save
  // as new set. A press meanwhile does nothing.
  const busy = ref(false);
  const confirmingClose = ref(false);

  // --- the custom icons the icon pickers work with -----------------------

  // A name resolves to the diagram's copy of an icon, else to the server's
  // icon library's.
  provide(INSPECTOR_ICONS, {
    entry(name) {
      const icon = resolveIcon(
        name,
        diagram ? store.doc?.icons : null,
        iconLibrary,
      );

      return icon ? { name, ...icon } : undefined;
    },
    diagram: () =>
      diagram
        ? Object.entries(store.doc?.icons || {}).map(([name, entry]) => ({
            name,
            data: entry?.data,
          }))
        : [],
  });
  provide(INSPECTOR_ICON_LIBRARY, iconLibrary);

  // --- the set of the library, in library mode -----------------------------

  const editedSet = props.setId ? store.findLayerSet(props.setId) : null;
  const builtin = Boolean(editedSet?.builtin);
  // A set that the user cannot change: a built-in one.
  const viewOnly = !diagram && Boolean(props.setId) && builtin;
  // The version the edit starts from. A save after a later read of the
  // library must still be refused when someone saved the set meanwhile.
  const editedETag = editedSet?.etag || '';

  // The copy the dialog edits: the set's content, or a new set with the
  // placeholder layers.
  const local = reactive({
    name: editedSet?.name || '',
    description: editedSet?.description || '',
    iconKey: editedSet?.iconKey || '',
    icon: editedSet?.icon || '',
    matchConflict: editedSet?.matchConflict || MATCH_CONFLICT_LOWEST,
    layerOutlines: editedSet?.layerOutlines === true,
    layers: editedSet ? [...editedSet.layers] : [],
  });

  function contentOf(value) {
    return layerSetContent({
      ...value,
      layers: effectiveLayerList(value.layers),
    });
  }

  const initialContent = JSON.stringify(contentOf(local));
  const dirty = computed(
    () => !viewOnly && JSON.stringify(contentOf(local)) !== initialContent,
  );

  // --- what the rows edit ------------------------------------------------

  const readOnly = computed(() => Boolean(store.readOnly));
  const locked = computed(() => (diagram ? readOnly.value : viewOnly));
  const layers = computed(() =>
    diagram ? store.doc?.layers || [] : local.layers,
  );
  const placeholders = computed(() => layers.value.length === 0);
  const options = computed(() =>
    diagram
      ? layerSetOptions(store.doc)
      : {
          matchConflict: local.matchConflict,
          layerOutlines: local.layerOutlines,
        },
  );

  function said(entry) {
    return entry ? `${entry.label}.` : '';
  }

  // How a layer of the set is named in a message, as the store names one.
  function nameOf(id) {
    return layerName({ layers: local.layers }, id);
  }

  // The words for the fields a change of a layer changes.
  function fieldNames(changes) {
    const names = Object.keys(changes)
      .map(
        (key) =>
          ({
            label: 'label',
            iconKey: 'icon',
            icon: 'icon',
            color: 'color',
            matchers: 'node matchers',
          })[key],
      )
      .filter((name, at, list) => name && list.indexOf(name) === at);

    return names.join(' and ');
  }

  const diagramOps = {
    add() {
      const layer = store.addLayer();

      return layer
        ? { layer, message: `Added ${placeholderLabel(layer.id)}.` }
        : null;
    },
    renumber: (from, to) => said(store.renumberLayer(from, to)),
    update(id, changes) {
      const keys = Object.keys(changes);

      return said(
        keys.length === 1 && keys[0] === 'label'
          ? store.relabelLayer(id, changes.label)
          : store.updateLayer(id, changes),
      );
    },
    remove: (id) => said(store.removeLayer(id)),
  };

  // Each change of the copy, as a sentence, or '' when it changed nothing.
  function changeLocal(next, message) {
    if (!next) {
      return '';
    }

    local.layers = next;

    return message;
  }

  const libraryOps = {
    add() {
      const added = withLayerAdded(local.layers);

      if (!added) {
        return null;
      }

      local.layers = added.layers;

      return {
        layer: added.layer,
        message: `Added ${placeholderLabel(added.layer.id)}.`,
      };
    },
    renumber: (from, to) =>
      changeLocal(
        withLayerRenumbered(local.layers, from, to),
        `Changed the ID of ${nameOf(from)} to ${to}.`,
      ),
    update: (id, changes) =>
      changeLocal(
        withLayerUpdated(local.layers, id, changes),
        changes.label !== undefined && Object.keys(changes).length === 1
          ? `Renamed ${nameOf(id)} to ${changes.label}.`
          : `Changed the ${fieldNames(changes)} of ${nameOf(id)}.`,
      ),
    remove: (id) =>
      changeLocal(withLayerRemoved(local.layers, id), `Removed ${nameOf(id)}.`),
  };

  function setOption(changes) {
    if (locked.value) {
      return;
    }

    if (diagram) {
      const entry = store.setLayerSetOptions(changes);

      if (entry) {
        status.set(`${entry.label}.`);
      }

      return;
    }

    Object.assign(local, changes);

    if ('layerOutlines' in changes) {
      status.set(
        changes.layerOutlines
          ? 'Node outlines take the layer color.'
          : 'Node outlines do not take the layer color.',
      );
    } else {
      status.set(
        changes.matchConflict === MATCH_CONFLICT_HIGHEST
          ? 'The highest layer ID wins.'
          : 'The lowest layer ID wins.',
      );
    }
  }

  // --- the nodes at each layer, in diagram mode ---------------------------

  const counts = computed(() => layerCounts(store.doc));

  // Layer IDs that devices or switches are at and that the list does not
  // have.
  const unlisted = computed(() => {
    if (!diagram) {
      return '';
    }

    const listed = new Set(effectiveLayerList(layers.value).map((l) => l.id));
    const ids = [...counts.value.keys()]
      .filter((id) => !listed.has(id))
      .sort((a, b) => b - a);

    return ids.length
      ? `Some nodes are at layer IDs that are not in this list: ${ids.join(', ')}. The editor shows each one as "Layer N".`
      : '';
  });

  // --- the link to a layer set, in diagram mode ----------------------------

  const sets = computed(() => store.layerSets.items);
  const link = computed(() => store.layerSetLink);
  const failed = computed(
    () => !store.layerSets.loaded && store.layerSets.status === 'failed',
  );

  function setTitle(set) {
    return set.builtin ? `${set.name} (built-in)` : set.name;
  }

  const linkText = computed(() => {
    const { status: state, set, source } = link.value;
    const name =
      set?.name || layerSetOptions(store.doc).name || source?.id || '';

    if (failed.value && state === 'unknown') {
      return `Could not load the layer sets. ${store.layerSets.error}`.trim();
    }

    switch (state) {
      case 'unknown':
        return 'Loading the layer sets…';
      case 'linked':
        return `Linked to layer set ${name}, version ${source.version}.`;
      case 'changed':
        return `Linked to layer set ${name}, version ${source.version}. You changed the layers in this diagram after the copy.`;
      case 'update':
        return `Layer set ${name} has a newer version, ${set.version}. This diagram has a copy of version ${source.version}.`;
      case 'missing':
        return `Linked to layer set ${name}, which the server does not have now.`;
      default:
        return 'These layers are not linked to a layer set.';
    }
  });

  const canSaveToSet = computed(
    () => link.value.status === 'changed' && !link.value.set?.builtin,
  );

  // The set Use a layer set shows first: the set the layers are linked to,
  // else the server's default set, else the first.
  const chosen = ref('');

  function chooseFirst() {
    const ids = sets.value.map((set) => set.id);
    const preferred = [
      link.value.source?.id,
      store.layerSets.defaultId,
      ids[0],
    ].find((id) => id && ids.includes(id));

    chosen.value = preferred || '';
  }

  // A library read after the dialog opened (Retry) gives the choices.
  watch(sets, (list) => {
    if (!list.some((set) => set.id === chosen.value)) {
      chooseFirst();
    }
  });

  function apply() {
    const set = store.findLayerSet(chosen.value);

    if (!set || readOnly.value) {
      return;
    }

    const entry = store.applyLayerSet(set.id);

    status.set(
      entry
        ? `${entry.label}.`
        : `The layers are a copy of layer set ${set.name} already.`,
    );
  }

  // Focus goes to the control of that test ID once the dialog shows the
  // change: the button that had it can go.
  async function focusAfterChange(testId) {
    await nextTick();
    document.querySelector(`[data-testid="${testId}"]`)?.focus();
  }

  // Retry goes while the library loads. Focus that went with it goes back
  // to it when the read fails again, else to Use a layer set, else to Save
  // as new set….
  async function retry() {
    await store.fetchLayerSets();
    await nextTick();

    if (!focusLost()) {
      return;
    }

    const target = [
      '[data-testid="layers-retry"]',
      '#layers-choose:not(:disabled)',
      '[data-testid="layers-save-new"]',
    ]
      .map((selector) => document.querySelector(selector))
      .find(Boolean);

    target?.focus();
  }

  function updateFromSet() {
    const entry = store.updateFromLayerSet();

    if (entry) {
      status.set(`${entry.label}.`);
      // The button goes once the copy is the set's version.
      focusAfterChange('layers-save-new');
    }
  }

  function unlink() {
    const entry = store.unlinkLayerSet();

    if (entry) {
      status.set(`${entry.label}.`);
      // The button goes with the link.
      focusAfterChange('layers-save-new');
    }
  }

  function warningsText(warnings) {
    return warnings?.length ? ` ${warnings.join(' ')}` : '';
  }

  async function saveToSet() {
    if (busy.value) {
      return;
    }

    busy.value = true;
    error.clear();

    try {
      const { set, warnings } = await store.saveToLayerSet();

      status.set(
        `Saved the layers to layer set ${set.name}, version ${set.version}.${warningsText(warnings)}`,
      );
      // The button goes once the copy is the set's version.
      await nextTick();
      document.querySelector('[data-testid="layers-save-new"]')?.focus();
    } catch (caught) {
      error.set(store.describeLayerSetError(caught, 'save the layer set'));
    } finally {
      busy.value = false;
    }
  }

  // Save as new set…: a name and a description for the new set.
  const naming = ref(false);
  const newSet = reactive({ name: '', description: '' });

  async function toggleNaming() {
    naming.value = !naming.value;
    error.clear();

    if (naming.value) {
      const name = layerSetOptions(store.doc).name || 'Layer set';

      newSet.name = uniqueLayerSetName(
        name,
        sets.value.map((set) => set.name),
      );
      newSet.description = '';
      await nextTick();
      newName.value?.select();
    } else {
      // Cancel goes with the form.
      focusAfterChange('layers-save-new');
    }
  }

  async function saveAsNew() {
    if (busy.value) {
      return;
    }

    const name = newSet.name.trim();
    const problem =
      layerSetNameProblem(name) ||
      layerSetDescriptionProblem(newSet.description.trim());

    if (problem) {
      fieldError.fail(
        problem,
        layerSetNameProblem(name) ? 'name' : 'description',
      );

      return;
    }

    busy.value = true;
    error.clear();

    try {
      const { set, warnings } = await store.saveAsNewLayerSet({
        name,
        description: newSet.description.trim(),
      });

      naming.value = false;
      status.set(
        `Added layer set ${set.name}, and linked the diagram to it.${warningsText(warnings)}`,
      );
      await nextTick();
      document.querySelector('[data-testid="layers-save-new"]')?.focus();
    } catch (caught) {
      error.set(store.describeLayerSetError(caught, 'add the layer set'));
    } finally {
      busy.value = false;
    }
  }

  // --- Save and Cancel, in library mode ------------------------------------

  async function save() {
    if (busy.value || viewOnly) {
      return;
    }

    const name = local.name.trim();
    const description = local.description.trim();
    const nameProblem = layerSetNameProblem(name);
    const problem = nameProblem || layerSetDescriptionProblem(description);

    if (problem) {
      fieldError.fail(problem, nameProblem ? 'name' : 'description');

      return;
    }

    if (rowsEl.value?.unsettled) {
      error.set(
        'Some layer fields have errors. Correct them, and then select Save.',
      );
      rowsEl.value.focusError();

      return;
    }

    const content = contentOf({ ...local, name, description });
    const layersError = layersProblem(content.layers);

    if (layersError) {
      error.set(layersError);

      return;
    }

    busy.value = true;
    error.clear();

    try {
      const set = props.setId
        ? await store.saveLayerSet(props.setId, content, editedETag)
        : (await store.createLayerSet(content)).set;

      emit('saved', { set, created: !props.setId });
      emit('close');
    } catch (caught) {
      error.set(
        store.describeLayerSetError(
          caught,
          props.setId ? 'save the layer set' : 'add the layer set',
        ),
      );
    } finally {
      busy.value = false;
    }
  }

  function requestClose() {
    if (!diagram && dirty.value) {
      confirmingClose.value = true;
    } else {
      emit('close');
    }
  }

  const title = computed(() => {
    if (diagram) {
      return 'Layers of this diagram';
    }

    if (!props.setId) {
      return 'New layer set';
    }

    // The name as the set has it: the title stays while the name changes.
    const name = editedSet?.name || '';

    return viewOnly ? `Layer set ${name}` : `Edit layer set ${name}`;
  });

  // The library is read again as the dialog opens, so a newer version of
  // the linked set shows. A choice made meanwhile stays (see the watch of
  // sets).
  onMounted(() => {
    if (diagram) {
      chooseFirst();
      store.fetchLayerSets();
    }
  });
</script>

<style scoped>
  /* As tall as its content, up to the dialog's limit: then the fields
     scroll, and the buttons stay in view under them. */
  .builder-layers-dialog[open] {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  /* The box that scrolls clips what is outside it, so it reaches 0.4rem
     past the fields on each side, and a focus ring of a field at an edge
     shows whole. */
  .builder-layers-dialog__body {
    flex: 1 1 auto;
    min-height: 0;
    margin: -0.4rem;
    padding: 0.4rem;
    overflow-y: auto;
  }

  .builder-layers-dialog__intro {
    margin: 0 0 0.25rem;
  }

  .builder-layers-dialog__heading {
    margin: 1rem 0 0.4rem;
    padding-top: 0.6rem;
    border-top: 1px solid var(--bx-border);
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--bx-text-muted);
  }

  .builder-layers-dialog__link {
    margin: 0 0 0.4rem;
    overflow-wrap: anywhere;
  }

  .builder-layers-dialog__actions,
  .builder-layers-dialog__row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.6rem;
  }

  .builder-layers-dialog__row {
    flex-wrap: nowrap;
    margin-bottom: 0;
  }

  .builder-layers-dialog__row select {
    flex: 1 1 auto;
    min-width: 0;
  }

  .builder-layers-dialog__row .builder-button {
    flex: none;
  }

  .builder-layers-dialog__new {
    margin: 0 0 0.75rem;
    padding: 0.6rem;
  }

  .builder-layers-dialog__label {
    display: block;
    margin-bottom: 0.2rem;
    font-size: 0.85rem;
    font-weight: 600;
  }

  .builder-layers-dialog .builder-hint {
    margin: 0.2rem 0 0;
  }

  .builder-layers-dialog__footer {
    flex: none;
    margin-top: 0.75rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--bx-border);
  }
</style>
