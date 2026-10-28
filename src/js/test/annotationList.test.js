// The annotation list (a v-model: each change is a new list) of the create and
// VM details cards: each row, its problem or description, and the add menu.
import fc from 'fast-check';
import { afterEach, describe, expect, it, vi } from 'vitest';

import AnnotationList from '@/components/AnnotationList.vue';
import {
  annotationRow,
  CUSTOM_EXPERIMENT_ANNOTATION,
  CUSTOM_NODE_ANNOTATION,
  EXPERIMENT_ANNOTATIONS,
  NODE_ANNOTATIONS,
} from '@/utils/experimentAnnotations.js';
import { byLabel, mountComponent, textOf } from './helpers/mount.js';

afterEach(() => {
  vi.restoreAllMocks();
});

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
const list = (modelValue, extra) =>
  mountComponent(AnnotationList, {
    props: {
      modelValue,
      idPrefix: 'node-annotations',
      label: 'Annotations for Every VM',
      help: 'Added to every VM',
      catalog: NODE_ANNOTATIONS,
      custom: CUSTOM_NODE_ANNOTATION,
      ...extra,
    },
  });

// Each row as drawn: its key (in brackets when typed in), how its value is
// entered, and the line under it, marked when it names a problem.
const rows = (wrapper) =>
  wrapper.findAll('.annotation-row').map((row) => {
    const fixed = row.find('code.annotation-key');
    const value = row.find('.annotation-value select, .annotation-value input');
    const line = row.find('.annotation-help');
    return {
      key: fixed.exists()
        ? textOf(fixed)
        : `[${row.find('.annotation-key input').element.value}]`,
      value:
        value.element.tagName === 'SELECT'
          ? 'true or false'
          : value.attributes('type'),
      line: (line.classes('is-danger') ? 'problem: ' : '') + textOf(line),
    };
  });

// the add menu's entries, which the dropdown moves to the page body
const menuItems = (wrapper) => [
  ...wrapper
    .findComponent({ name: 'BDropdown' })
    .vm.$refs.dropdownMenu.querySelectorAll('.annotation-add-item'),
];

// the menu's entries in order, whether a line divides them, and which way
// the menu opens
const menu = (wrapper) => {
  const [first] = menuItems(wrapper);
  return {
    entries: menuItems(wrapper).map((item) => textOf(item.querySelector('b'))),
    divided: Boolean(first.parentElement.querySelector('.dropdown-divider')),
    opens: wrapper.find('.dropdown').classes('is-top-right') ? 'top' : 'bottom',
  };
};

describe('an annotation list', () => {
  it('draws each row with an input suited to it, and says what it does', async () => {
    const wrapper = await list([
      known('phenix/default-apps'),
      known('vrouter/vyos-password', 'secret'),
      known('phenix/startup-autotunnel', '8080'),
      custom('team', 'red'),
    ]);

    expect(rows(wrapper)).toEqual([
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
    const wrapper = await list([
      custom(''),
      known('phenix/default-apps', false),
    ]);

    expect(rows(wrapper).map((row) => row.line)).toEqual([
      'problem: Enter a key',
      entry('phenix/default-apps').description,
    ]);
  });

  it('offers a custom annotation alone once every known one is listed, and an experiment list its own', async () => {
    const all = NODE_ANNOTATIONS.map((e) => annotationRow(e));
    expect(menu(await list(all))).toMatchObject({
      entries: ['Custom annotation'],
      divided: false,
    });

    // an experiment list offers the experiment annotations
    const experiment = {
      catalog: EXPERIMENT_ANNOTATIONS,
      custom: CUSTOM_EXPERIMENT_ANNOTATION,
    };
    expect(menu(await list([], experiment)).entries).toEqual([
      'phenix.workflow/tags',
      'Custom annotation',
    ]);
  });

  it('offers, for any rows, the known annotations they lack, then a custom one', async () => {
    const keys = NODE_ANNOTATIONS.map((e) => e.key);
    // a known row, or a custom one with a key typed with space around it
    const rowArb = fc.oneof(
      fc.constantFrom(...keys).map((key) => ({ key })),
      fc.record({
        key: fc.constantFrom(...keys, 'team'),
        space: fc.stringMatching(/^ {0,2}$/),
      }),
    );
    await fc.assert(
      fc.asyncProperty(fc.array(rowArb, { maxLength: 6 }), async (picked) => {
        const wrapper = await list(
          picked.map(({ key, space }) =>
            space === undefined ? known(key) : custom(space + key + space),
          ),
        );
        const lacking = keys.filter((k) => !picked.some((r) => r.key === k));
        expect(menu(wrapper)).toEqual({
          entries: [...lacking, 'Custom annotation'],
          divided: lacking.length > 0,
          opens: 'bottom',
        });
        wrapper.unmount();
      }),
      { numRuns: 20 },
    );
  });

  describe('its menu', () => {
    // where the menu opens once its trigger, this far down, is clicked
    const opensAt = async (top) => {
      const wrapper = await list([]);
      const trigger = wrapper.find('.dropdown-trigger button');
      vi.spyOn(trigger.element, 'getBoundingClientRect').mockReturnValue({
        top,
        bottom: top + 30,
      });
      await trigger.trigger('click');
      const { opens } = menu(wrapper);
      wrapper.unmount();
      return opens;
    };

    it('opens upward when there is no room for it below', async () => {
      vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(800);
      expect(await opensAt(100)).toBe('bottom');
      expect(await opensAt(700)).toBe('top');
    });

    // the menu's max-height in AnnotationList.vue
    const MENU = 360;

    it('opens upward from the first pixel too few for it below', async () => {
      vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(800);
      // the trigger is 30 high, so this leaves exactly MENU below it
      const fits = 800 - MENU - 30;
      expect(await opensAt(fits)).toBe('bottom');
      expect(await opensAt(fits + 1)).toBe('top');
    });

    it('opens upward only when there is too little room below and more above', async () => {
      const height = vi.spyOn(window, 'innerHeight', 'get');
      const room = fc.integer({ min: 0, max: 1500 });
      const short = fc.integer({ min: 0, max: MENU - 1 });
      // the room above and below the trigger, and which way the menu opens
      const layout = fc.oneof(
        fc
          .tuple(room, fc.integer({ min: MENU, max: 1500 }))
          .map(([above, below]) => ({ above, below, opens: 'bottom' })),
        short.chain((below) =>
          fc.oneof(
            fc
              .integer({ min: below + 1, max: below + 1500 })
              .map((above) => ({ above, below, opens: 'top' })),
            fc
              .integer({ min: 0, max: below })
              .map((above) => ({ above, below, opens: 'bottom' })),
          ),
        ),
      );
      await fc.assert(
        fc.asyncProperty(layout, async ({ above, below, opens }) => {
          height.mockReturnValue(above + 30 + below);
          expect(await opensAt(above)).toBe(opens);
        }),
        { numRuns: 30 },
      );
    });
  });

  describe('changes', () => {
    // the list with two rows, and each new list it asks for; the rows it was
    // given must stay as they were
    async function twoRows() {
      const given = [known('phenix/default-apps'), custom('team')];
      const before = structuredClone(given);
      const wrapper = await list(given);
      const lists = () => {
        expect(given).toEqual(before);
        return wrapper.emitted('update:modelValue').map(([value]) => value);
      };
      return { wrapper, given, lists };
    }

    it('add a known annotation with its default, or an empty custom one', async () => {
      const { wrapper, given, lists } = await twoRows();
      const click = (name) =>
        menuItems(wrapper)
          .find((item) => textOf(item.querySelector('b')) === name)
          .click();

      click('phenix/startup-via-cc');
      click('Custom annotation');

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

    it("change a row's key or value", async () => {
      const { wrapper, given, lists } = await twoRows();

      await byLabel(wrapper, 'Annotation 2 key').setValue('owner');
      await byLabel(wrapper, 'phenix/default-apps value').setValue('true');

      expect(lists()).toEqual([
        [given[0], { ...given[1], key: 'owner' }],
        [{ ...given[0], value: true }, given[1]],
      ]);
    });

    it('remove a row', async () => {
      const { wrapper, given, lists } = await twoRows();

      await byLabel(wrapper, 'Remove phenix/default-apps').trigger('click');
      expect(lists()).toEqual([[given[1]]]);
    });
  });
});
