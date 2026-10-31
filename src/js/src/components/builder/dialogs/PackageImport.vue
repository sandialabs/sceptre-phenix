<!-- The needs of an uploaded Builder package (POST /builder/package/resolve).
     Only a carried config that the server lacks can be created, never replaced. -->
<template>
  <div ref="root" data-testid="upload-package">
    <p id="upload-package-summary" class="builder-package__summary">
      {{ summary }}
    </p>

    <section
      v-for="group in groups"
      :key="group.kind"
      class="builder-package__group"
      :aria-labelledby="`upload-package-${group.kind}`"
      :data-testid="`upload-package-group-${group.kind}`">
      <h3 :id="`upload-package-${group.kind}`" class="builder-package__title">
        {{ group.label }}
      </h3>
      <ul class="builder-package__list">
        <li
          v-for="(dependency, index) in group.items"
          :key="dependencyKey(dependency)"
          class="builder-package__item"
          :data-testid="`upload-package-item-${dependency.kind}-${dependency.name}`">
          <span
            :id="`upload-package-name-${group.kind}-${index}`"
            class="builder-package__name">
            {{ dependency.name }}
          </span>
          <!-- Separators for screen readers, which would read the boxes'
               words run together. -->
          <span class="builder-visually-hidden">{{ ': ' }}</span>
          <span
            class="builder-package__status"
            :data-testid="`upload-package-status-${dependency.kind}-${dependency.name}`">
            {{ statusText(dependency) }}
          </span>
          <template v-if="dependency.detail">
            <span class="builder-visually-hidden">{{ '. ' }}</span>
            <span class="builder-package__detail">{{ dependency.detail }}</span>
          </template>
          <label v-if="canCreate(dependency)" class="builder-choice">
            <input
              v-model="ticked"
              type="checkbox"
              :value="dependencyKey(dependency)"
              :aria-describedby="`upload-package-name-${group.kind}-${index}`"
              :data-testid="`upload-package-create-${dependency.kind}-${dependency.name}`" />
            Create on this server
          </label>
        </li>
      </ul>
    </section>

    <p v-if="!groups.length">The package lists nothing the diagram needs.</p>

    <!-- What the dialog is doing, on screen. The dialog's own status
         announces it. -->
    <p
      v-if="busy && progress"
      id="upload-package-progress"
      class="builder-hint"
      data-testid="upload-package-progress">
      {{ progress }}
    </p>

    <!-- A busy button keeps focus, so neither is disabled: each is
         aria-disabled and does nothing while the dialog works. -->
    <div class="builder-dialog__actions">
      <button
        type="button"
        class="builder-button"
        data-testid="upload-package-cancel"
        :aria-disabled="busy ? 'true' : undefined"
        :aria-describedby="cancelDescription"
        @click="cancel">
        Cancel
      </button>
      <button
        ref="continueButton"
        type="button"
        class="builder-button builder-button--primary"
        aria-describedby="upload-package-summary"
        data-testid="upload-package-continue"
        :aria-disabled="busy ? 'true' : undefined"
        :aria-busy="busy || undefined"
        @click="proceed">
        Continue to editor
      </button>
    </div>
  </div>
</template>

<script setup>
  import { computed, ref } from 'vue';

  import { count } from '@/builder/announce.js';
  import {
    DEPENDENCY_STATUS_TEXT,
    canCreate,
    dependencyKey,
    groupDependencies,
    tickedDependencies,
  } from '@/builder/package.js';

  const props = defineProps({
    // What the server says the diagram needs (see readDependencies).
    dependencies: { type: Array, required: true },
    // Whether the dialog is creating the ticked configs.
    busy: { type: Boolean, default: false },
    // What the dialog is doing while busy, such as "Creating 2 configs on
    // this server…".
    progress: { type: String, default: '' },
  });

  const emit = defineEmits(['cancel', 'continue']);

  const root = ref(null);
  const continueButton = ref(null);
  // The keys (see dependencyKey) of the configs ticked for creation. None
  // is ticked at first.
  const ticked = ref([]);

  const groups = computed(() => groupDependencies(props.dependencies));

  // While the dialog works, Cancel is described by what it is doing, which
  // says why Cancel does nothing.
  const cancelDescription = computed(() =>
    props.busy && props.progress ? 'upload-package-progress' : undefined,
  );

  const summary = computed(() => {
    const all = props.dependencies.length;
    const missing = props.dependencies.filter(
      (dependency) => dependency.status === 'missing',
    ).length;
    const creatable = props.dependencies.some(canCreate);

    return [
      `The diagram needs ${count(all, 'item')}. ${missing} of them ${missing === 1 ? 'is' : 'are'} missing on this server.`,
      creatable
        ? 'Tick each missing config that the package carries to create it on this server.'
        : '',
      'The Builder does not replace anything on the server.',
    ]
      .filter(Boolean)
      .join(' ');
  });

  // The status in words, and whether the package carries the item.
  function statusText(dependency) {
    const status = DEPENDENCY_STATUS_TEXT[dependency.status];

    return dependency.packaged ? `${status}, in the package` : status;
  }

  function proceed() {
    // A busy button keeps focus, so it can still be pressed.
    if (props.busy) {
      return;
    }

    emit('continue', tickedDependencies(props.dependencies, ticked.value));
  }

  // While the configs are created, Cancel would leave that work running out
  // of sight. So Cancel does nothing until the work is done.
  function cancel() {
    if (props.busy) {
      return;
    }

    emit('cancel');
  }

  // Where the dialog moves focus once the list replaces its form: the first
  // checkbox, else Continue to editor.
  function focusFirst() {
    const box = root.value?.querySelector('input[type="checkbox"]');

    (box || continueButton.value)?.focus();
  }

  defineExpose({ focusFirst });
</script>

<style scoped>
  /* The summary and each kind's list are apart by the same gap. */
  .builder-package__summary,
  .builder-package__group {
    margin-bottom: 0.75rem;
  }

  .builder-package__title {
    font-size: 1rem;
    font-weight: 600;
    margin-bottom: 0.25rem;
  }

  .builder-package__list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  /* Name, status and detail on a line that wraps, the checkbox after them. */
  .builder-package__item {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem 0.75rem;
    padding: 0.25rem 0;
    border-bottom: 1px solid var(--bx-border);
  }

  .builder-package__name {
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  /* The status is a word in a box, in the text's own color. */
  .builder-package__status {
    padding: 0 0.375rem;
    border: 1px solid currentColor;
    border-radius: 0.25rem;
  }

  .builder-package__detail {
    flex-basis: 100%;
  }
</style>
