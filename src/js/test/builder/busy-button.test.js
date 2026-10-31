// @vitest-environment happy-dom
// The busy button (BuilderBusyButton.vue): a ring while busy, the same name
// and focus, and no second press.

import { afterEach, describe, expect, test } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { enableAutoUnmount, mount } from '@vue/test-utils';

import BuilderBusyButton from '@/components/builder/BuilderBusyButton.vue';

enableAutoUnmount(afterEach);

function button(props, label = 'PNG') {
  return mount(BuilderBusyButton, {
    attachTo: document.body,
    props,
    slots: { default: () => label },
  });
}

const ring = (wrapper) => wrapper.find('[data-testid="builder-spinner"]');

describe('the busy button', () => {
  test('with an icon, the ring takes the icon’s place and the name stays', async () => {
    const wrapper = button({ icon: 'image' });

    expect(wrapper.find('.builder-icon--image').exists()).toBe(true);
    expect(ring(wrapper).exists()).toBe(false);
    expect(wrapper.attributes('aria-busy')).toBeUndefined();

    await wrapper.setProps({ busy: true });

    expect(wrapper.find('.builder-icon').exists()).toBe(false);
    expect(ring(wrapper).attributes('aria-hidden')).toBe('true');
    expect(wrapper.text()).toBe('PNG');
    expect(wrapper.attributes('aria-label')).toBeUndefined();
    expect(wrapper.attributes('aria-busy')).toBe('true');
    expect(wrapper.attributes('aria-disabled')).toBe('true');
    expect(wrapper.attributes('disabled')).toBeUndefined();
  });

  test('without an icon, the ring turns over the label, which still names it', async () => {
    const wrapper = button({ busy: true }, 'Publishing…');
    const label = wrapper.get('.builder-busy-button__label');

    expect(ring(wrapper).exists()).toBe(true);
    expect(label.text()).toBe('Publishing…');
    expect(label.attributes('aria-hidden')).toBeUndefined();
    expect(wrapper.attributes('aria-label')).toBeUndefined();
    expect(wrapper.classes()).not.toContain('is-replaced');
  });

  test('a busy label names it while busy instead', () => {
    const wrapper = button({ busy: true, busyLabel: 'Loading nera' }, 'View');

    expect(wrapper.attributes('aria-label')).toBe('Loading nera');
    expect(
      wrapper.get('.builder-busy-button__label').attributes('aria-hidden'),
    ).toBe('true');
    expect(wrapper.classes()).toContain('is-replaced');
  });

  test('a press while busy does nothing and keeps focus, and a busy submit sends no form', async () => {
    let submits = 0;
    const busy = ref(true);
    const form = mount(
      {
        render: () =>
          h(
            'form',
            {
              onSubmit: (event) => {
                event.preventDefault();
                submits += 1;
              },
            },
            [
              h(
                BuilderBusyButton,
                { type: 'submit', busy: busy.value },
                () => 'Go',
              ),
            ],
          ),
      },
      { attachTo: document.body },
    );
    const submit = form.getComponent(BuilderBusyButton);

    submit.vm.focus();
    await submit.trigger('click');

    expect(submit.emitted('click')).toBeUndefined();
    expect(submits).toBe(0);
    expect(document.activeElement).toBe(submit.element);
    expect(submit.attributes('type')).toBe('submit');

    busy.value = false;
    await nextTick();
    await submit.trigger('click');
    expect(submit.emitted('click')).toHaveLength(1);
    expect(submits).toBe(1);
  });

  test('disabled keeps focus and passes the press on, locked disables it', async () => {
    const off = button({ disabled: true });

    expect(off.attributes('aria-disabled')).toBe('true');
    expect(off.attributes('disabled')).toBeUndefined();
    await off.trigger('click');
    expect(off.emitted('click')).toHaveLength(1);

    const locked = button({ locked: true });

    expect(locked.attributes('aria-disabled')).toBe('true');
    expect(locked.attributes('disabled')).toBe('');
  });
});
