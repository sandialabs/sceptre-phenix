// @vitest-environment happy-dom
// The Configs editor keeps a legacy Builder diagram (builder-xml) on save,
// and Edit on a Builder topology opens the Builder or shows an alert.

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import Buefy from 'buefy';
import YAML from 'js-yaml';

const site = vi.hoisted(() => ({
  apiVersion: 'phenix.sandia.gov/v1',
  kind: 'Topology',
  metadata: { name: 'site', annotations: { 'builder-doc': '{}' } },
  spec: { nodes: [] },
}));

vi.mock('@/utils/axios.js', () => ({
  default: {
    get: vi.fn(() => Promise.resolve({ data: structuredClone(site) })),
  },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));
const phenix = vi.hoisted(() => ({ role: null }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => phenix }));

import ConfigsEditor from '@/components/configs/ConfigsEditor.vue';

const XML =
  '<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel>';

function topology(annotations) {
  return {
    apiVersion: 'phenix.sandia.gov/v1',
    kind: 'Topology',
    metadata: annotations ? { name: 'old', annotations } : { name: 'old' },
    spec: { nodes: [] },
  };
}

// The editor's state and the two methods that turn a config into the text
// it shows and back, without the page around them.
function editor(config, lang = 'yaml') {
  const state = {
    editor: { lang },
    config: { obj: config, str: '', builderXML: null },
    getConfigObj: ConfigsEditor.methods.getConfigObj,
    getConfigStr: ConfigsEditor.methods.getConfigStr,
  };

  state.config.str = state.getConfigStr(lang);

  return state;
}

describe('the Configs text editor and a legacy Builder diagram', () => {
  test.each(['yaml', 'json'])('the %s text leaves the diagram out', (lang) => {
    const state = editor(topology({ 'builder-xml': XML, owner: 'ops' }), lang);

    expect(state.config.str).not.toContain('mxGraphModel');
    expect(state.config.str).toContain('<SNIPPED>');
    expect(state.config.str).toContain('ops');
    expect(state.config.builderXML).toBe(XML);
  });

  test.each(['yaml', 'json'])(
    'a save from %s writes the diagram back as it was, beside what was edited',
    (lang) => {
      const state = editor(
        topology({ 'builder-xml': XML, owner: 'ops' }),
        lang,
      );

      state.config.str = state.config.str.replace('ops', 'e2e');

      expect(state.getConfigObj().metadata.annotations).toEqual({
        'builder-xml': XML,
        owner: 'e2e',
      });
      // Read again, as each check of the text does: still the diagram.
      expect(state.getConfigObj().metadata.annotations['builder-xml']).toBe(
        XML,
      );
    },
  );

  test('an empty diagram is written back empty, not as the placeholder', () => {
    const state = editor(topology({ 'builder-xml': '' }));

    expect(state.config.str).toContain('builder-xml: <SNIPPED>');
    expect(state.getConfigObj().metadata.annotations).toEqual({
      'builder-xml': '',
    });
  });

  test('changing the file format keeps the diagram', () => {
    const state = editor(topology({ 'builder-xml': XML }));
    const convert = ConfigsEditor.methods.convertLang.bind(state);

    convert('json');
    expect(state.editor.lang).toBe('json');
    expect(JSON.parse(state.config.str).metadata.annotations).toEqual({
      'builder-xml': '<SNIPPED>',
    });
    expect(state.getConfigObj().metadata.annotations['builder-xml']).toBe(XML);

    convert('yaml');
    expect(state.config.str).toContain('builder-xml: <SNIPPED>');
    expect(state.getConfigObj().metadata.annotations['builder-xml']).toBe(XML);
  });

  // The text is what is saved: only the placeholder stands for the diagram.
  test('a line the user removed or rewrote is saved as typed', () => {
    const removed = editor(topology({ 'builder-xml': XML, owner: 'ops' }));

    removed.config.str = removed.config.str.replace(
      /^ *builder-xml: <SNIPPED>\n/m,
      '',
    );
    expect(removed.getConfigObj().metadata.annotations).toEqual({
      owner: 'ops',
    });

    const rewritten = editor(topology({ 'builder-xml': XML }));

    rewritten.config.str = rewritten.config.str.replace(
      '<SNIPPED>',
      "'<mxGraphModel/>'",
    );
    expect(rewritten.getConfigObj().metadata.annotations).toEqual({
      'builder-xml': '<mxGraphModel/>',
    });
  });

  test('text without the annotations, or that is no config, is read as it is', () => {
    const state = editor(topology({ 'builder-xml': XML }));

    state.config.str = YAML.dump(topology());
    expect(state.getConfigObj()).toEqual(topology());

    for (const text of ['', 'just words', '- a\n- b\n', 'metadata: 3']) {
      state.config.str = text;
      expect(() => state.getConfigObj(), text).not.toThrow();
    }

    state.config.str = 'nodes: [unclosed';
    expect(state.getConfigObj()).toBeNull();
  });

  test('a topology without a legacy diagram is shown and read unchanged', () => {
    const plain = topology({ owner: 'ops' });
    const state = editor(structuredClone(plain));

    expect(state.config.str).not.toContain('SNIPPED');
    expect(state.config.builderXML).toBeNull();
    expect(state.getConfigObj()).toEqual(plain);
    // The placeholder typed into such a config stands for nothing.
    state.config.str = YAML.dump(topology({ 'builder-xml': '<SNIPPED>' }));
    expect(state.getConfigObj().metadata.annotations).toEqual({
      'builder-xml': '<SNIPPED>',
    });
  });
});

// roleAllowed caches its answers by role name, so every role gets its own.
let roles = 0;
function setRole(resources) {
  roles += 1;
  phenix.role = {
    name: `editor-role-${roles}`,
    policies: [{ resources, verbs: ['*'], resourceNames: ['*/*'] }],
  };
}

function mountEditor(router) {
  return mount(ConfigsEditor, {
    attachTo: document.body,
    props: { mode: 'edit', editorConfig: structuredClone(site) },
    global: {
      plugins: [Buefy],
      stubs: { AceEditor: true },
      mocks: { $router: router },
    },
  });
}

const alertText = 'Only Builder can edit this configuration';

describe('ConfigsEditor Edit on a Builder topology', () => {
  let router;

  beforeEach(() => {
    router = { push: vi.fn(() => Promise.resolve()) };
    document.body.innerHTML = '';
  });

  test('a role with the Builder goes to the Builder, with no alert', async () => {
    setRole(['*']);
    const wrapper = mountEditor(router);
    await flushPromises();

    expect(router.push).toHaveBeenCalledTimes(1);
    expect(wrapper.emitted('is-done')).toEqual([['']]);
    expect(document.body.textContent).not.toContain(alertText);
  });

  test('a role without the Builder gets the alert and stays on Configs', async () => {
    setRole(['configs']);
    const wrapper = mountEditor(router);
    await flushPromises();

    expect(router.push).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain(alertText);
    expect(document.body.textContent).toContain('Built by Builder');
    // The editor stays open behind the alert until the user closes it.
    expect(wrapper.emitted('is-done')).toBeUndefined();

    const ok = [...document.querySelectorAll('.modal-card button')].find(
      (button) => button.textContent.trim() === 'OK',
    );
    ok.click();
    await flushPromises();

    expect(wrapper.emitted('is-done')).toEqual([['']]);
    expect(router.push).not.toHaveBeenCalled();
  });
});
