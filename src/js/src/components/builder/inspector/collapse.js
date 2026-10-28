// Inspector sections, lists, maps and list entries that the user collapses. The
// state is kept by data path for each Inspector variant, for the session.

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
 * The collapsed state of one Inspector variant, which BuilderInspector provides
 * as INSPECTOR_COLLAPSE.
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
 * The collapsed state of one section, list or entry, by its data path. `errors`
 * says whether it holds a field with an error.
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
