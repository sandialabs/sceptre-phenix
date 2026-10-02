// How the Configs page's text editor treats the diagram of the legacy
// Builder a topology may still carry (its builder-xml annotation): the text
// shows a placeholder for it, and a save writes it back as it was.

import { describe, expect, test, vi } from 'vitest';
import YAML from 'js-yaml';

vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: () => {} }));

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
