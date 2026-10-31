// The Inspector mounted in the page on a new Builder store, for the happy-dom
// unit files, with helpers that act on its fields as a user does.

import { reactive } from 'vue';
import { createPinia } from 'pinia';
import { flushPromises, mount } from '@vue/test-utils';
import { JsonForms } from '@jsonforms/vue';

import BuilderInspector from '@/components/builder/BuilderInspector.vue';
import { settleIcons } from '@/builder/icons.js';
import { addNode, createDocument } from '@/builder/model.js';
import { builderSchemaV1 } from '@/builder/schema.js';
import { useBuilderStore } from '@/builder/store.js';

// What store.select takes for `element`: a connection has ends.
function selectionOf(element) {
  return element.sourceNodeId
    ? { edges: [element.id] }
    : { nodes: [element.id] };
}

// Mounts `component` (the Inspector by default) with `doc` loaded, so undo
// returns to it, and `element` selected. `prepare` sets the store up first.
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

// A host as the template editor makes one: a document, its one node, and a
// commit that replaces the document, recording commits and announcements.
export function fakeHost(doc, nodeId, init = {}) {
  const host = reactive({
    doc,
    inspectorSelection: { type: 'node', id: nodeId },
    schema: builderSchemaV1,
    schemaError: '',
    readOnly: false,
    disks: null,
    issues: [],
    canRedo: false,
    commits: [],
    said: [],
    commit(next, label) {
      host.doc = settleIcons(next, null);
      host.commits.push(label);

      return true;
    },
    announce(message) {
      host.said.push(message);
    },
    addInterface() {},
    removeInterface() {},
    remove() {},
    moveNodes() {},
    ...init,
  });

  return host;
}

// A document that holds one device, as the template editor's does.
export function oneDevice(options = {}) {
  const added = addNode(createDocument({ name: 'Scratch' }), {
    kind: 'device',
    hostname: 'plc',
    ...options,
  });

  return { doc: added.doc, node: added.node };
}
