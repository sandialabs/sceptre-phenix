// @vitest-environment happy-dom
// The dialog that asks which tab's changes to save to a draft, mounted and
// driven as a user would.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import BuilderTabsDialog from '@/components/builder/BuilderTabsDialog.vue';
import { choiceRows } from '@/builder/tabs.js';

enableAutoUnmount(afterEach);

afterEach(() => {
  vi.restoreAllMocks();
});

// This tab's version with `pending` changes, and the others' `versions`.
const rowsOf = (pending, versions) =>
  choiceRows({
    tab: 't1',
    pending,
    changedAt: '2026-10-16T10:00:00Z',
    versions,
  });

const OTHER = {
  id: 'tab:t2',
  where: 'tab',
  changes: 1,
  changedAt: '2026-10-16T09:00:00Z',
};
const CLOSED = {
  id: 'closed:k1',
  where: 'closed',
  changes: 3,
  changedAt: '2026-10-16T11:00:00Z',
};

async function open(props) {
  const wrapper = mount(BuilderTabsDialog, { attachTo: document.body, props });

  await flushPromises();

  return wrapper;
}

const radio = (wrapper, where) =>
  wrapper.get(`[data-testid="tabs-choice-row-${where}"] input[type="radio"]`);
const save = (wrapper) =>
  wrapper.get('[data-testid="tabs-choice-save"]').trigger('click');

describe('choosing which changes to save', () => {
  test("this tab's version is picked when it has changes, focus starts on it, and Save chooses it", async () => {
    const wrapper = await open({ rows: rowsOf(2, [OTHER, CLOSED]) });

    expect(wrapper.get('dialog').attributes('aria-describedby')).toBe(
      'tabs-choice-message',
    );
    expect(wrapper.findAll('label').map((label) => label.text())).toEqual([
      'This tab: 2 changes, last at 2026-10-16T10:00:00Z',
      'Another tab: 1 change, last at 2026-10-16T09:00:00Z',
      'A closed tab: 3 changes, last at 2026-10-16T11:00:00Z',
    ]);
    expect(radio(wrapper, 'this').element.checked).toBe(true);
    expect(document.activeElement).toBe(radio(wrapper, 'this').element);

    await save(wrapper);
    expect(wrapper.emitted('choose')).toEqual([['tab:t1']]);
  });

  test('without changes here, the version changed last is picked; another can be picked, and Decide later chooses none', async () => {
    const wrapper = await open({ rows: rowsOf(0, [OTHER, CLOSED]) });

    expect(radio(wrapper, 'closed').element.checked).toBe(true);
    expect(document.activeElement).toBe(radio(wrapper, 'closed').element);

    await radio(wrapper, 'tab').setValue();
    await save(wrapper);
    expect(wrapper.emitted('choose')).toEqual([['tab:t2']]);

    await wrapper.get('[data-testid="tabs-choice-later"]').trigger('click');
    expect(wrapper.emitted('close')).toHaveLength(1);
    expect(wrapper.emitted('choose')).toHaveLength(1);
  });

  test('Save does nothing while the choice is applied', async () => {
    const wrapper = await open({ rows: rowsOf(2, [OTHER]), busy: true });

    expect(
      wrapper
        .get('[data-testid="tabs-choice-save"]')
        .attributes('aria-disabled'),
    ).toBe('true');
    await save(wrapper);
    expect(wrapper.emitted('choose')).toBeUndefined();
  });

  test('a version that goes away is replaced by the one now preferred, which takes the focus it had', async () => {
    const wrapper = await open({ rows: rowsOf(0, [OTHER, CLOSED]) });

    expect(document.activeElement).toBe(radio(wrapper, 'closed').element);

    // The closed tab's queue was sent elsewhere: its row and radio go.
    await wrapper.setProps({ rows: rowsOf(0, [OTHER]) });
    await flushPromises();

    expect(
      wrapper.find('[data-testid="tabs-choice-row-closed"]').exists(),
    ).toBe(false);
    expect(radio(wrapper, 'tab').element.checked).toBe(true);
    expect(document.activeElement).toBe(radio(wrapper, 'tab').element);
    await save(wrapper);
    expect(wrapper.emitted('choose')).toEqual([['tab:t2']]);
  });
});

describe('Download', () => {
  test("is offered for this tab's and closed tabs' changes only, saves the file from inside the dialog and says so", async () => {
    const createObjectURL = vi
      .spyOn(URL, 'createObjectURL')
      .mockReturnValue('blob:lab');
    const downloadVersion = vi.fn(async (row) => ({
      name: `${row.where}.json`,
      text: `{"from":"${row.id}"}`,
    }));
    const wrapper = await open({
      rows: rowsOf(2, [
        OTHER,
        CLOSED,
        { ...CLOSED, id: 'closed:k2', changes: 0 },
      ]),
      downloadVersion,
    });
    const downloads = wrapper
      .findAll('button')
      .filter((button) => button.text() === 'Download');

    expect(downloads.map((button) => button.attributes('aria-label'))).toEqual([
      "Download this tab's changes",
      'Download the changes of closed tab 1',
    ]);

    await downloads[1].trigger('click');
    await flushPromises();

    expect(downloadVersion).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'closed:k1' }),
    );
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(await createObjectURL.mock.calls[0][0].text()).toBe(
      '{"from":"closed:k1"}',
    );
    expect(wrapper.get('[role="status"]').text()).toBe('Saved closed.json.');
    expect(wrapper.find('a').exists()).toBe(false);
    // Downloading chooses nothing, and leaves the dialog open.
    expect(wrapper.emitted('choose')).toBeUndefined();
    expect(wrapper.emitted('close')).toBeUndefined();

    downloadVersion.mockResolvedValueOnce(null);
    await downloads[0].trigger('click');
    await flushPromises();

    expect(wrapper.get('[role="status"]').text()).toBe(
      'This browser no longer holds these changes.',
    );
  });

  test('is not offered without a way to read the versions', async () => {
    const wrapper = await open({ rows: rowsOf(2, [CLOSED]) });

    expect(
      wrapper
        .findAll('button')
        .filter((button) => button.text() === 'Download'),
    ).toHaveLength(0);
  });
});
