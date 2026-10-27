import { describe, expect, test, vi } from 'vitest';
import StateOfHealth from '@/views/StateOfHealth.vue';
import VMLabelsModal from '@/components/VMLabelsModal.vue';

// The real modules import the Pinia store, the router or Buefy, which need a
// browser. Vitest hoists vi.mock above the imports.
vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));
vi.mock('@/utils/rbac.js', () => ({ roleAllowed: vi.fn(() => true) }));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: vi.fn(),
  removeWsHandler: vi.fn(),
}));
vi.mock('@/store.js', () => ({ usePhenixStore: vi.fn() }));

const topologyNotes = ['Domain controller.'];
const tags = { '__notes_2026-10-27T12:00:00.000Z': 'VM note.' };

// newView runs the view's options against a plain object.
function newView() {
  const view = {
    ...StateOfHealth.data(),
    $route: { params: { id: 'exp' } },
    $buefy: { modal: { open: vi.fn() } },
  };
  for (const [name, method] of Object.entries(StateOfHealth.methods)) {
    view[name] = method.bind(view);
  }
  return view;
}

describe('State of Health topology notes', () => {
  test('keeps the node notes for the details dialog', () => {
    const view = newView();

    view.clicked(null, {
      label: 'host-01',
      image: 'linux',
      status: 'running',
      soh: null,
      tags,
      notes: topologyNotes,
    });

    expect(view.detailsModal.notes).toEqual(topologyNotes);
    expect(view.detailsModal.tags).toBe(tags);
  });

  test('uses no notes when the node has none', () => {
    const view = newView();

    view.clicked(null, {
      label: 'host-01',
      image: 'linux',
      status: 'running',
      soh: null,
      tags: {},
    });

    expect(view.detailsModal.notes).toEqual([]);
  });

  test('passes the notes to the Labels dialog', () => {
    const view = newView();

    view.showTagsModal('host-01', tags, topologyNotes);

    expect(view.$buefy.modal.open).toHaveBeenCalledWith(
      expect.objectContaining({
        component: VMLabelsModal,
        props: {
          vmName: 'host-01',
          experiment: 'exp',
          tags,
          topologyNotes,
        },
      }),
    );
  });
});
