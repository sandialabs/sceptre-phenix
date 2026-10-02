// The editable list of annotations on the create experiment card and the VM
// details card: a row per annotation with its problem or its description
// under it, and a menu that adds a known annotation not in the list yet or a
// custom one. The list is a v-model: every change is a new list.
import { describe, expect, it, vi } from 'vitest';

import AnnotationList from '@/components/AnnotationList.vue';
import {
  annotationRow,
  CUSTOM_EXPERIMENT_ANNOTATION,
  CUSTOM_NODE_ANNOTATION,
  EXPERIMENT_ANNOTATIONS,
  NODE_ANNOTATIONS,
} from '@/utils/experimentAnnotations.js';
import { makeContext } from './helpers/context.js';
import { renderSSR, textOf } from './helpers/render.js';

const entry = (key) => NODE_ANNOTATIONS.find((e) => e.key === key);
const known = (key, value) => ({
  ...annotationRow(entry(key)),
  ...(value !== undefined && { value }),
});
const custom = (key, value = 'v') => ({
  ...annotationRow(CUSTOM_NODE_ANNOTATION),
  key,
  value,
});

// a node annotation list holding these rows
const props = (modelValue, extra) => ({
  modelValue,
  idPrefix: 'node-annotations',
  label: 'Annotations for Every VM',
  help: 'Added to every VM',
  catalog: NODE_ANNOTATIONS,
  custom: CUSTOM_NODE_ANNOTATION,
  ...extra,
});

const render = (modelValue, extra, data) =>
  renderSSR(AnnotationList, props(modelValue, extra), { data });

// Each row as drawn: its key (fixed, or in brackets when it is typed in),
// how its value is entered, and the line under it, marked when it names a
// problem.
async function rows(modelValue, extra) {
  const html = await render(modelValue, extra);
  return [
    ...html.matchAll(
      /<div class="annotation-\w+ annotation-row"[\s\S]*?<\/p>/g,
    ),
  ].map(([row]) => {
    const fixed = row.match(/<code class="annotation-key"[^>]*>([^<]*)</);
    const typed = row.match(
      /aria-label="Annotation \d+ key" value(?:="([^"]*)")?/,
    );
    const [, tag, attrs] = row.match(/<(select|input)\b([^>]*value"[^>]*)>/);
    const [, problem, line] = row.match(
      /<p class="help( is-danger)? annotation-help"[^>]*>([\s\S]*)<\/p>/,
    );
    return {
      key: fixed ? fixed[1] : `[${typed[1] ?? ''}]`,
      value:
        tag === 'select' ? 'true or false' : attrs.match(/type="(\w+)"/)[1],
      line: (problem ? 'problem: ' : '') + textOf(line),
    };
  });
}

// the menu's entries in order, whether a line divides them, and which way
// the menu opens
async function menu(modelValue, extra, data) {
  const html = await render(modelValue, extra, data);
  return {
    entries: [
      ...html.matchAll(
        /annotation-add-item[^>]*>(?:<!--\[-->)?<b[^>]*>([^<]*)</g,
      ),
    ].map(([, name]) => name),
    divided: html.includes('dropdown-divider'),
    opens: html.match(/class="dropdown [^"]*is-(top|bottom)-right/)[1],
  };
}

describe('an annotation list', () => {
  it('draws each row with an input suited to it, and says what it does', async () => {
    expect(
      await rows([
        known('phenix/default-apps'),
        known('vrouter/vyos-password', 'secret'),
        known('phenix/startup-autotunnel', '8080'),
        custom('team', 'red'),
      ]),
    ).toEqual([
      {
        key: 'phenix/default-apps',
        value: 'true or false',
        line: entry('phenix/default-apps').description,
      },
      {
        key: 'vrouter/vyos-password',
        value: 'password',
        line: entry('vrouter/vyos-password').description,
      },
      {
        key: 'phenix/startup-autotunnel',
        value: 'text',
        line: entry('phenix/startup-autotunnel').description,
      },
      {
        key: '[team]',
        value: 'text',
        line: CUSTOM_NODE_ANNOTATION.description,
      },
    ]);
  });

  it("puts a row's problem in place of its description", async () => {
    // annotationErrors decides which rows have a problem; this shows where
    const lines = (
      await rows([custom(''), known('phenix/default-apps', false)])
    ).map((row) => row.line);

    expect(lines).toEqual([
      'problem: Enter a key',
      entry('phenix/default-apps').description,
    ]);
  });

  it('offers only the known annotations not in the list yet, then a custom one', async () => {
    expect(
      await menu([
        known('phenix/default-apps'),
        custom(' vrouter/enable-ssh '),
      ]),
    ).toEqual({
      entries: [
        'phenix/startup-autotunnel',
        'phenix/startup-via-cc',
        'vrouter/vyos-password',
        'Custom annotation',
      ],
      divided: true,
      opens: 'bottom',
    });

    const all = NODE_ANNOTATIONS.map((e) => annotationRow(e));
    expect(await menu(all)).toMatchObject({
      entries: ['Custom annotation'],
      divided: false,
    });

    // an experiment list offers the experiment annotations
    const experiment = {
      catalog: EXPERIMENT_ANNOTATIONS,
      custom: CUSTOM_EXPERIMENT_ANNOTATION,
    };
    expect((await menu([], experiment)).entries).toEqual([
      'phenix.workflow/tags',
      'Custom annotation',
    ]);
  });

  it('opens its menu upward when there is no room for it below', async () => {
    // the menu's trigger, this far down an 800 pixel window
    const opensAt = async (top) => {
      const ctx = makeContext(AnnotationList, props([]));
      vi.stubGlobal('window', { innerHeight: 800 });
      ctx.placeMenu({
        currentTarget: {
          getBoundingClientRect: () => ({ top, bottom: top + 30 }),
        },
      });
      vi.unstubAllGlobals();
      return (await menu([], {}, { menuUp: ctx.menuUp })).opens;
    };

    expect(await opensAt(100)).toBe('bottom');
    expect(await opensAt(700)).toBe('top');
  });

  describe('changes', () => {
    // the list with two rows, and each new list it asks for; the rows it was
    // given must stay as they were
    function list() {
      const given = [known('phenix/default-apps'), custom('team')];
      const before = structuredClone(given);
      const $emit = vi.fn();
      const ctx = makeContext(AnnotationList, { ...props(given), $emit });
      const lists = () => {
        expect(given).toEqual(before);
        return $emit.mock.calls.map(([event, value]) => {
          expect(event).toBe('update:modelValue');
          return value;
        });
      };
      return { ctx, given, lists };
    }

    it('add a known annotation with its default, or an empty custom one', () => {
      const { ctx, given, lists } = list();

      ctx.add(entry('phenix/startup-via-cc'));
      ctx.add(CUSTOM_NODE_ANNOTATION);

      const [withKnown, withCustom] = lists();
      expect(withKnown.slice(0, 2)).toEqual(given);
      expect(withKnown[2]).toMatchObject({
        known: true,
        key: 'phenix/startup-via-cc',
        type: 'boolean',
        value: true,
      });
      expect(withCustom[2]).toMatchObject({ known: false, key: '', value: '' });
    });

    it("change a row's key or value", () => {
      const { ctx, given, lists } = list();

      ctx.update(1, { key: 'owner' });
      ctx.update(0, { value: true });

      expect(lists()).toEqual([
        [given[0], { ...given[1], key: 'owner' }],
        [{ ...given[0], value: true }, given[1]],
      ]);
    });

    it('remove a row', () => {
      const { ctx, given, lists } = list();

      ctx.remove(0);
      expect(lists()).toEqual([[given[1]]]);
    });
  });
});
