// Sections and list entries of the Inspector that the user collapses: a
// group of fields (General, Hardware, ...), a list (Interfaces, Drives,
// ...), a map (Labels, ...) and each entry of a list (Interface 1: eth0).
// Each has a disclosure button in its header (InspectorDisclosure.vue).
// All start expanded.
//
// Which are collapsed is kept by data path for each variant of the
// Inspector (beside the canvas, the template editor, the Node Editor), for
// the rest of the session: the next node selected or opened shows the
// same sections collapsed, as the More settings section stays open (see
// InspectorSectionRenderer). Reset view expands them all again (see
// resetCollapsed).
//
// A collapsed section that holds a field with an error says so in its
// header. Going to the field (the error summary, Go to from the checks,
// the Node Editor's table of contents) expands it (see reveal). So does
// an Apply or a save that the error stops (see settle in
// BuilderInspector.vue).

import { computed, inject, reactive, unref } from 'vue';

import {
  afterInsert,
  afterMove,
  afterRemove,
  hasErrorUnder,
  pathContains,
} from '@/builder/formOutline.js';

// The collapsed paths, by variant ('canvas', 'template', 'editor').
const collapsed = reactive({});

function pathsOf(variant) {
  collapsed[variant] ??= new Set();

  return collapsed[variant];
}

/**
 * Reset view: every section and entry of every Inspector expands.
 */
export function resetCollapsed() {
  for (const variant of Object.keys(collapsed)) {
    collapsed[variant] = new Set();
  }
}

// Provided by BuilderInspector: the collapsed state of its variant (see
// useCollapseState), and the paths of the fields with errors that it
// shows. Renderers rendered anywhere else collapse nothing, and their
// headers mark no error.
export const INSPECTOR_COLLAPSE = Symbol('inspector-collapse');

/**
 * The collapsed state of one variant of the Inspector, which
 * BuilderInspector provides as INSPECTOR_COLLAPSE.
 *
 * @param {string} variant
 * @param {import('vue').Ref<string[]>|(() => string[])} errorPaths the
 *   paths of the fields with errors that the form shows
 */
export function useCollapseState(variant, errorPaths) {
  const paths = () => pathsOf(variant);
  const errors = computed(() =>
    typeof errorPaths === 'function' ? errorPaths() : unref(errorPaths),
  );

  function set(next) {
    collapsed[variant] = next;
  }

  return {
    isCollapsed: (path) => paths().has(path),
    collapsedPaths: () => [...paths()],
    setCollapsed(path, value) {
      if (paths().has(path) === value) {
        return;
      }

      const next = new Set(paths());

      if (value) {
        next.add(path);
      } else {
        next.delete(path);
      }

      set(next);
    },
    // Collapses each of `list`, or expands every one.
    collapseAll(list) {
      set(new Set(list));
    },
    expandAll() {
      set(new Set());
    },
    // Expands what holds the field at `path`, and the field itself if it
    // is a section. Returns whether anything expanded.
    reveal(path) {
      const shown = [...paths()].filter(
        (collapsedPath) => !pathContains(collapsedPath, path),
      );

      if (shown.length === paths().size) {
        return false;
      }

      set(new Set(shown));

      return true;
    },
    // A list's entries change places: each keeps its own state.
    inserted: (listPath, index) => set(afterInsert(paths(), listPath, index)),
    removed: (listPath, index) => set(afterRemove(paths(), listPath, index)),
    moved: (listPath, from, to) => set(afterMove(paths(), listPath, from, to)),
    hasErrors: (scope) => hasErrorUnder(scope, errors.value),
  };
}

const NONE = Object.freeze({
  isCollapsed: () => false,
  collapsedPaths: () => [],
  setCollapsed: () => {},
  collapseAll: () => {},
  expandAll: () => {},
  reveal: () => false,
  inserted: () => {},
  removed: () => {},
  moved: () => {},
  hasErrors: () => false,
});

export function useInspectorCollapse() {
  return inject(INSPECTOR_COLLAPSE, NONE);
}

/**
 * The collapsed state of one section, list or entry, by its data path.
 *
 * @param {import('vue').MaybeRef<string>|(() => string)} path
 * @returns {{collapsed: import('vue').ComputedRef<boolean>, errors:
 *   import('vue').ComputedRef<boolean>, toggle: () => void}} `errors` says
 *   whether a field with an error is inside it
 */
export function useCollapsible(path) {
  const state = useInspectorCollapse();
  const key = computed(() =>
    typeof path === 'function' ? path() : unref(path),
  );
  const collapsed = computed(() => state.isCollapsed(key.value));
  const errors = computed(() => state.hasErrors(key.value));

  function toggle() {
    state.setCollapsed(key.value, !collapsed.value);
  }

  return { collapsed, errors, toggle };
}
