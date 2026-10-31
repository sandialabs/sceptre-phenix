// Mounts components with @vue/test-utils, the real Buefy and the app's named
// routes, and reads back what they draw; for files run in happy-dom.
import { createWrapperError, flushPromises, mount } from '@vue/test-utils';
import { h } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';

import { installBuefy } from '@/utils/buefy.js';
import { ROUTES } from './routes.js';

// draws each icon as the name it asks for
const Icon = {
  props: ['icon'],
  render: (ctx) => h('i', { 'data-icon': [ctx.icon].flat().at(-1) }),
};

// Mounts `component` at `route`; `data` is set over its data(). The wrapper
// also has the `router`.
export async function mountComponent(
  component,
  { props, data, route = '/', stubs, slots, attachTo } = {},
) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: ROUTES,
  });
  await router.push(route);
  const wrapper = mount(component, {
    props,
    slots,
    attachTo,
    ...(data && { data: () => data }),
    global: {
      plugins: [installBuefy, router],
      components: {
        'font-awesome-icon': Icon,
      },
      stubs,
    },
  });
  return Object.assign(wrapper, { router });
}

// a route-level page, mounted, once the requests it made as it opened are
// answered by the mocks
export async function mountPage(component, options) {
  const wrapper = await mountComponent(component, options);
  await flushPromises();
  return wrapper;
}

// the text of a wrapper or element, with runs of space collapsed
export const textOf = (node) =>
  (node instanceof Node ? node.textContent : node.text())
    .replace(/\s+/g, ' ')
    .trim();

// the element with this aria-label, compared as text since happy-dom
// rejects escaped quotes in a selector
export const byLabel = (wrapper, label) =>
  wrapper
    .findAll('[aria-label]')
    .find((el) => el.attributes('aria-label') === label) ??
  createWrapperError('DOMWrapper');

// whether an element is disabled; Vue drops the attribute when it is not
// disabled, or greyed out with aria-disabled and still focusable
const disabled = (el) =>
  el.attributes('disabled') !== undefined ||
  el.attributes('aria-disabled') === 'true';

// Each button: its text or aria-label, plus ' (submits)', ' (spinning)' or
// ' (disabled)' when it submits, spins or is disabled.
export const buttons = (wrapper) =>
  wrapper
    .findAll('button')
    .map(
      (b) =>
        (textOf(b) || b.attributes('aria-label') || '') +
        (b.attributes('type') === 'submit' ? ' (submits)' : '') +
        (b.classes('is-loading') ? ' (spinning)' : '') +
        (disabled(b) ? ' (disabled)' : ''),
    );

// each body row of a Buefy table: its cells by column label
export const tableRows = (wrapper) =>
  wrapper
    .findAll('tbody tr')
    .map((tr) =>
      Object.fromEntries(
        tr
          .findAll('td[data-label]')
          .map((td) => [td.attributes('data-label'), td]),
      ),
    )
    .filter((cells) => Object.keys(cells).length);

// a Buefy table's header cell for the column with this label; click to sort
export const tableHeader = (wrapper, label) =>
  wrapper.findAll('thead th').find((th) => textOf(th) === label);

// where each Buefy tooltip in `node` opens, such as 'is-left'
export const tooltipPositions = (node) =>
  node
    .findAllComponents({ name: 'BTooltip' })
    .map((tooltip) => tooltip.props('position'));

// Each Buefy tooltip's text with its control's aria-label and disabled state,
// read from the component because append-to-body moves the text.
export const tooltipButtons = (wrapper) =>
  wrapper.findAllComponents({ name: 'BTooltip' }).map((tooltip) => {
    const control = tooltip.find('.tooltip-trigger').find('button, a');
    return {
      tooltip: textOf(tooltip.vm.$refs.content),
      label: control.attributes('aria-label') ?? '',
      disabled: disabled(control),
      control,
    };
  });

// the Buefy dialogs open on the page body, oldest first
export const dialogs = () =>
  [...document.querySelectorAll('.dialog')].filter(
    (dialog) => dialog.style.display !== 'none',
  );

// Clicks the newest dialog's confirm button, typing `value` into its prompt
// first, and waits for what that set off.
export async function confirmDialog(value) {
  const dialog = dialogs().at(-1);
  if (value !== undefined) {
    const input = dialog.querySelector('input');
    input.value = value;
    input.dispatchEvent(new Event('input'));
  }
  dialog.querySelector('footer .button:last-child').click();
  await flushPromises();
}

// Each Buefy toast on the page body, oldest first: its text and color.
export const toasts = () =>
  [...document.querySelectorAll('.toast')].map((toast) => ({
    text: textOf(toast),
    type: [...toast.classList].find((c) =>
      /^is-(dark|light|primary|link|info|success|warning|danger)$/.test(c),
    ),
  }));
