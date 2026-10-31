// @vitest-environment happy-dom
// The Node Editor says the store's announcements in its own status region,
// and records them, so the page's live region does not say them again after
// the dialog closes. Save states stay with the page.

import { afterEach, describe, expect, test } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import NodeEditorDialog from '@/components/builder/dialogs/NodeEditorDialog.vue';
import { unsaidPart } from '@/builder/announce.js';
import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';

enableAutoUnmount(afterEach);

describe('the Node Editor', () => {
  test('says and records what the store announces, but not save states', async () => {
    const pinia = createPinia();
    const store = useBuilderStore(pinia);
    const { doc, alpha } = sampleDocument();

    store.commit(doc, '');

    const wrapper = mount(NodeEditorDialog, {
      attachTo: document.body,
      props: { nodeId: alpha.id },
      global: { plugins: [pinia] },
    });

    await flushPromises();

    store.announce('Moved alpha');
    const moved = { text: 'Moved alpha', seq: store.announcementSeq };

    await flushPromises();

    expect(wrapper.get('[data-testid="node-editor-status"]').text()).toBe(
      'Moved alpha',
    );
    expect(unsaidPart(moved, store.dialogSaid)).toBe('');

    store.announce('All changes saved.', { slot: 'save' });
    const saved = { text: 'All changes saved.', seq: store.announcementSeq };

    await flushPromises();

    expect(wrapper.get('[data-testid="node-editor-status"]').text()).toBe(
      'Moved alpha',
    );
    expect(unsaidPart(saved, store.dialogSaid)).toBe(saved.text);
  });
});
