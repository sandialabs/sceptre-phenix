// The options Import offers for a topology (see builderGenerateChoices in
// web/builder_sources.go). A config file is parsed here to count its includes.

import { computed, reactive, watch } from 'vue';

import { count } from '@/builder/announce.js';
import { LEGACY_ANNOTATION, uploadedConfigInfo } from '@/builder/configs.js';
import { uniqueName } from '@/builder/ids.js';
import { configName, configNameProblem } from '@/builder/publish.js';
import { utf8Length } from '@/builder/text.js';
import { MAX_NAME_BYTES } from '@/builder/validate.js';

function nameOf(entry) {
  return typeof entry === 'string' ? entry : entry?.name || '';
}

/** What the dialog says of the topologies that a topology includes. */
export function includesHint(includes, name = '') {
  return `${name || 'This topology'} includes ${count(includes, 'other topology', 'other topologies')}.`;
}

/** The proposed name: "site-copy" or "site-combined", numbered when taken. */
export function proposedName(base, mode, taken) {
  const suffix = mode === 'combine' ? 'combined' : 'copy';

  return uniqueName(`${configName(base) || 'topology'}-${suffix}`, taken);
}

/**
 * Why `name` cannot name the new topology, or ''. The server checks it again
 * (builderGenerateNewName in web/builder_sources.go).
 */
export function newNameProblem(name, taken) {
  const problem = configNameProblem('topology', name);

  if (problem) {
    return problem;
  }

  if (utf8Length(name) > MAX_NAME_BYTES) {
    return `Enter a name of at most ${MAX_NAME_BYTES} bytes.`;
  }

  return taken.includes(name)
    ? `A topology named ${name} already exists. Enter another name.`
    : '';
}

/** The intro when a Configs page link opens a topology with no diagram. */
export function importIntro(name, legacy) {
  return legacy
    ? `Topology ${name} has a legacy Builder diagram. Import it to convert the diagram.`
    : `Topology ${name} has no Builder diagram to open. Import it to make one.`;
}

/** What the dialog says when a linked topology is not one it offers. */
export function notAvailable(name) {
  return `Topology ${name} is not available to import. It does not exist, or you do not have permission to read it.`;
}

/**
 * The Import dialog's options for its form's source, as a reactive object.
 * Its request() gives the argument of store.generate().
 */
export function useImportOptions(form, topologies) {
  const state = reactive({
    includes: 'keep',
    copy: false,
    // What the user typed as the new name, or null while it follows the
    // proposal.
    typed: null,
  });

  const names = computed(() => (topologies() || []).map(nameOf));
  const chosen = computed(() =>
    form.source === 'stored' && form.kind === 'topology' && form.name
      ? (topologies() || []).find((entry) => nameOf(entry) === form.name)
      : null,
  );
  // The chosen file, when it is a Topology config.
  const file = computed(() => {
    const info =
      form.source === 'file' && form.content
        ? uploadedConfigInfo(form.content)
        : null;

    return info?.kind === 'topology' ? info : null;
  });

  const stored = computed(() => Boolean(chosen.value));
  const base = computed(() =>
    stored.value ? form.name : file.value?.name || '',
  );
  const includeCount = computed(() =>
    stored.value
      ? Number(chosen.value?.includeCount) || 0
      : file.value?.includes || 0,
  );
  const showIncludes = computed(() => includeCount.value > 0);
  const combine = computed(
    () => showIncludes.value && state.includes === 'combine',
  );
  // Combine makes a new topology already. A draft of a config file is never
  // linked to a stored topology, so it has nothing to be a copy of.
  const showCopy = computed(() => stored.value && !combine.value);
  const mode = computed(() => {
    if (combine.value) {
      return 'combine';
    }

    return showCopy.value && state.copy ? 'copy' : 'import';
  });
  const proposal = computed(() =>
    mode.value === 'import'
      ? ''
      : proposedName(base.value, mode.value, names.value),
  );
  const newName = computed(() => state.typed ?? proposal.value);

  // The choices apply to one source. Another source starts from the
  // defaults and from the name proposed for it.
  watch(
    () => [form.source, form.kind, form.name, form.content],
    () => {
      state.includes = 'keep';
      state.copy = false;
      state.typed = null;
    },
    { flush: 'sync' },
  );

  return reactive({
    includes: computed({
      get: () => state.includes,
      set: (value) => {
        state.includes = value === 'combine' ? 'combine' : 'keep';
      },
    }),
    copy: computed({
      get: () => state.copy,
      set: (value) => {
        state.copy = Boolean(value);
      },
    }),
    includeCount,
    includesText: computed(() =>
      includesHint(includeCount.value, stored.value ? form.name : ''),
    ),
    showIncludes,
    showCopy,
    copyOf: computed(() => (stored.value ? form.name : '')),
    showNewName: computed(() => mode.value !== 'import'),
    newName,
    mode,
    legacy: computed(() => chosen.value?.builder === LEGACY_ANNOTATION),

    typeName(text) {
      state.typed = String(text ?? '');
    },

    problem() {
      return mode.value === 'import'
        ? ''
        : newNameProblem(newName.value.trim(), names.value);
    },

    request() {
      const request =
        form.source === 'file'
          ? { content: form.content }
          : { kind: form.kind, name: form.name };

      if (showIncludes.value) {
        request.includes = state.includes;
      }

      if (mode.value === 'copy') {
        request.copy = true;
      }

      if (mode.value !== 'import') {
        request.newName = newName.value.trim();
      }

      return request;
    },
  });
}
