// Command contexts for the command tests: a store and a view whose actions
// are spies, built on sampleDocument.

import { vi } from 'vitest';

import { VIEW_API, createCommandContext } from '@/builder/commands.js';

import { sampleDocument, withTemplates } from './fixtures.js';

export const PLATFORMS = ['mac', 'other'];

function fakeStore(overrides = {}) {
  const { doc } = sampleDocument();
  const store = {
    doc,
    readOnly: false,
    selection: { nodes: [], edges: [] },
    clipboard: null,
    canUndo: false,
    canRedo: false,
    canRestoreLayout: false,
    theme: 'system',
    history: { undoLabel: () => 'Added device' },
    drafts: { all: [] },
    documents: [],
    ...overrides,
  };

  for (const action of [
    'announce',
    'undo',
    'redo',
    'copy',
    'paste',
    'duplicate',
    'group',
    'ungroup',
    'selectAll',
    'clearSelection',
    'layout',
    'restoreLayout',
    'autoGroup',
    'saveNow',
    'addNode',
    'connect',
    'removeSelection',
    'setNodesLayer',
  ]) {
    store[action] = vi.fn();
  }
  store.select = vi.fn((selection) => {
    store.selection = { nodes: [], edges: [], ...selection };
  });

  return withTemplates(store);
}

function fakeView(overrides = {}) {
  const view = {
    editing: true,
    showMinimap: true,
    snapToGrid: false,
    minimapSize: { width: 200, min: 120, max: 400 },
    panes: { hidden: { start: false, end: false }, stacked: false },
    canZoomIn: true,
    canZoomOut: true,
    fitRestores: false,
    focusMode: false,
    ...overrides,
  };

  for (const name of VIEW_API) {
    if (!(name in view)) {
      view[name] = vi.fn();
    }
  }

  return view;
}

export function context({ store = {}, view = {} } = {}) {
  return createCommandContext({
    store: fakeStore(store),
    view: fakeView(view),
  });
}
