// The options Import offers for a topology: what to do with the topologies
// it includes, and whether the diagram is a copy. Both make a diagram that
// is linked to no config and has a new name, so publishing it creates a new
// topology (see builderGenerateChoices in web/builder_sources.go).
//
// The dialog knows what to offer before the server reads anything. A stored
// topology's entry in the sources says how many topologies it includes. For
// a config file, this module parses the file to get the same count.

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

/**
 * What the dialog says of the topologies a topology includes.
 *
 * @param {number} includes how many it includes
 * @param {string} [name] the stored topology's name, or none for a config
 *   file
 * @returns {string} "site includes 1 other topology.", or "This topology
 *   includes 3 other topologies."
 */
export function includesHint(includes, name = '') {
  return `${name || 'This topology'} includes ${count(includes, 'other topology', 'other topologies')}.`;
}

/**
 * The name proposed for the new topology: the imported one's with "-copy"
 * or "-combined", numbered when a stored topology has that name already.
 *
 * @param {string} base the imported topology's name
 * @param {'copy'|'combine'} mode
 * @param {string[]} taken the names of the stored topologies
 * @returns {string} "site-copy", then "site-copy-2"
 */
export function proposedName(base, mode, taken = []) {
  const suffix = mode === 'combine' ? 'combined' : 'copy';

  return uniqueName(`${configName(base) || 'topology'}-${suffix}`, taken);
}

/**
 * Why `name` cannot name the new topology, or '' when it can. The server
 * checks the name again (builderGenerateNewName in web/builder_sources.go).
 *
 * @param {string} name trimmed name
 * @param {string[]} taken the names of the stored topologies
 * @returns {string}
 */
export function newNameProblem(name, taken = []) {
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

/**
 * What the dialog says above its form when a link from the Configs page
 * opened it on a topology that has no diagram to open.
 *
 * @param {string} name the topology
 * @param {boolean} legacy whether it has a diagram of the legacy Builder
 * @returns {string}
 */
export function importIntro(name, legacy) {
  return legacy
    ? `Topology ${name} has a legacy Builder diagram. Import it to convert the diagram.`
    : `Topology ${name} has no Builder diagram to open. Import it to make one.`;
}

/**
 * What the dialog says when the topology a link named is not one it offers.
 *
 * @param {string} name the topology
 * @returns {string}
 */
export function notAvailable(name) {
  return `Topology ${name} is not available to import: it does not exist, or you may not read it.`;
}

/**
 * The options of the Import dialog for the source its form holds.
 *
 * @param {object} form the dialog's form: source ('stored' or 'file'), kind,
 *   name and content
 * @param {() => Array} topologies the stored topologies, as the sources
 *   list them
 * @returns {object} a reactive object with these members:
 *   - includes ('keep' or 'combine') and copy: the choices, as the form's
 *     controls hold them.
 *   - includeCount and includesText (read only): for the "Included
 *     topologies" group, which shows while showIncludes is true.
 *   - showCopy and copyOf (read only): copyOf is the topology that the copy
 *     leaves as it is.
 *   - showNewName and newName (read only).
 *   - mode (read only): 'import', 'copy' or 'combine'.
 *   - legacy (read only): the chosen stored topology has a legacy Builder
 *     diagram.
 *   - typeName(text): takes what the user types as the new name. The name
 *     then stops following the proposal.
 *   - problem(): why the new name cannot be used. '' when it can, or when
 *     the dialog asks for no new name.
 *   - request(): the argument that store.generate() takes.
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
