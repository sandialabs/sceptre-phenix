// The Inspector mounted in the page on a new Builder store, for the happy-dom
// unit files, with helpers that act on its fields as a user does.

import { createPinia } from 'pinia';
import { flushPromises, mount } from '@vue/test-utils';
import { JsonForms } from '@jsonforms/vue';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import { useBuilderStore } from '@/builder/store.js';

// What store.select takes for `element`: a connection has ends.
export function selectionOf(element) {
  return element.sourceNodeId
    ? { edges: [element.id] }
    : { nodes: [element.id] };
}

// Mounts `component` (the Inspector by default) on a store holding `doc` as
// loaded, so an undo goes back to it, with `element` selected; `prepare`
// sets the store up first.
export async function mountInspector({
  doc,
  element,
  prepare,
  props,
  component = BuilderInspector,
} = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  if (doc) {
    store.commit(doc, '');
  }

  if (element) {
    store.select(selectionOf(element));
  }

  prepare?.(store);

  const wrapper = mount(component, {
    attachTo: document.body,
    props,
    global: { plugins: [pinia] },
  });

  await flushPromises();

  return { wrapper, store };
}

// The form's data as JSON Forms holds it, a copy.
export function formData(wrapper) {
  return JSON.parse(
    JSON.stringify(wrapper.findComponent(JsonForms).props('data')),
  );
}

// Sends `data` as the form's new data, as JSON Forms does once a control
// commits: for edits of several fields at once, which no one control makes.
export async function sendFormData(wrapper, data) {
  wrapper.findComponent(JsonForms).vm.$emit('change', { data, errors: [] });
  await flushPromises();
}

// The input, select or textarea of the field at `path`.
export function fieldAt(wrapper, path) {
  return wrapper.get(
    `[data-path="${path}"] :is(input:not([type="hidden"]), select, textarea)`,
  );
}

// Types `text` into `field` without committing it, as keystrokes do.
export async function type(field, text) {
  field.element.value = text;
  await field.trigger('input');
}

// Enter in a text field: the browser submits the form with its default
// button, which happy-dom does not do on its own.
export async function submitWithEnter(wrapper, field) {
  const apply = wrapper.find('[data-testid="inspector-apply"]');

  await field.trigger('keydown', { key: 'Enter' });
  field.element.form.requestSubmit(apply.exists() ? apply.element : null);
  await flushPromises();
}

// Dispatches a cancelable `key` keydown on `field`, and returns the event.
export function keydown(field, key) {
  const event = new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
  });

  field.element.dispatchEvent(event);

  return event;
}

// Whether Apply and Cancel show.
export function actionsShown(wrapper) {
  return wrapper.find('[data-testid="inspector-actions"]').exists();
}

// The disclosure button of the section, list or entry at `path`.
export function disclosureOf(wrapper, path) {
  return wrapper.get(
    `[data-collapse-path="${path}"] [data-testid="inspector-disclosure"]`,
  );
}
