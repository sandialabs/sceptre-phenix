// @vitest-environment happy-dom
// The item action button (BuilderActionButton.vue) and the confirmation that
// uses it: icon, label, color, the busy label, and no press while busy.

import { afterEach, describe, expect, test } from 'vitest';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderActionButton from '@/components/builder/BuilderActionButton.vue';
import BuilderConfirm from '@/components/builder/BuilderConfirm.vue';

enableAutoUnmount(afterEach);

function button(props, slots = {}) {
  return mount(BuilderActionButton, {
    attachTo: document.body,
    props,
    attrs: { 'data-testid': 'action' },
    slots,
  });
}

const shown = (wrapper) =>
  wrapper
    .findAll('.builder-button__swap > span')
    .filter((span) => !span.classes('is-off'))
    .map((span) => span.text());

describe('the item action button', () => {
  test('Delete is a red button with the trash icon and its label', () => {
    const wrapper = button({ action: 'delete' });

    expect(wrapper.element.tagName).toBe('BUTTON');
    expect(wrapper.attributes('type')).toBe('button');
    expect(wrapper.attributes('data-testid')).toBe('action');
    expect(wrapper.classes()).toEqual(
      expect.arrayContaining(['builder-button', 'builder-button--danger']),
    );
    expect(wrapper.find('.builder-icon--trash').exists()).toBe(true);
    expect(wrapper.text()).toBe('Delete');
  });

  test('Edit and Open are blue, and View has the default color', () => {
    for (const action of ['edit', 'open']) {
      expect(button({ action }).classes(), action).toContain(
        'builder-button--primary',
      );
    }

    for (const action of ['view', 'publish', 'download']) {
      const classes = button({ action }).classes();

      expect(classes, action).not.toContain('builder-button--primary');
      expect(classes, action).not.toContain('builder-button--danger');
    }

    expect(button({ action: 'view' }).find('.builder-icon--eye').exists()).toBe(
      true,
    );
    expect(
      button({ action: 'open' }).find('.builder-icon--folder-open').exists(),
    ).toBe(true);
  });

  test('a label of its own replaces the action’s', () => {
    const wrapper = button(
      { action: 'download' },
      { default: () => 'Download selected' },
    );

    expect(wrapper.text()).toBe('Download selected');
    expect(wrapper.find('.builder-icon--download').exists()).toBe(true);
  });

  test('while busy, a ring takes the icon’s place, the busy label shows, and a press does nothing', async () => {
    const wrapper = button({ action: 'open', busyText: 'Opening' });

    expect(shown(wrapper)).toEqual(['Open']);
    await wrapper.trigger('click');
    expect(wrapper.emitted('click')).toHaveLength(1);

    await wrapper.setProps({ busy: true });

    expect(shown(wrapper)).toEqual(['Opening']);
    expect(wrapper.find('.builder-icon').exists()).toBe(false);
    expect(wrapper.find('[data-testid="builder-spinner"]').exists()).toBe(true);
    expect(wrapper.attributes('aria-busy')).toBe('true');
    expect(wrapper.attributes('aria-disabled')).toBe('true');
    await wrapper.trigger('click');
    expect(wrapper.emitted('click')).toHaveLength(1);
  });

  test('disabled keeps focus: aria-disabled, not the disabled attribute', () => {
    const wrapper = button({ action: 'delete', disabled: true });

    expect(wrapper.attributes('aria-disabled')).toBe('true');
    expect(wrapper.attributes('disabled')).toBeUndefined();
  });

  test('focus() focuses the button', () => {
    const wrapper = button({ action: 'publish' });

    wrapper.vm.focus();
    expect(document.activeElement).toBe(wrapper.element);
  });
});

describe('the confirmation', () => {
  function confirm(props = {}) {
    return mount(BuilderConfirm, {
      attachTo: document.body,
      props: {
        title: 'Delete draft lab?',
        message: 'You cannot undo this.',
        confirmLabel: 'Delete draft',
        ...props,
      },
    });
  }

  const accept = (wrapper) => wrapper.get('[data-testid="confirm-accept"]');

  test('with the delete action, its button has the trash icon and stays red', async () => {
    const wrapper = confirm({ action: 'delete' });

    expect(accept(wrapper).classes()).toContain('builder-button--danger');
    expect(accept(wrapper).find('.builder-icon--trash').exists()).toBe(true);
    expect(accept(wrapper).text()).toBe('Delete draft');
    await accept(wrapper).trigger('click');
    expect(wrapper.emitted('confirm')).toHaveLength(1);
  });

  test('without an action, its button is red with no icon', () => {
    const wrapper = confirm();

    expect(accept(wrapper).classes()).toContain('builder-button--danger');
    expect(accept(wrapper).find('.builder-icon').exists()).toBe(false);
  });
});
