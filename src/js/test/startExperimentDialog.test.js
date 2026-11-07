// The start dialog of an experiment (StartExperimentDialog.vue): its options,
// their checks, the request body it gives, how confirmStart opens it, and the
// notifications that tell what a dry run or a start with warnings did.
import { flushPromises, mount } from '@vue/test-utils';
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { lastNotice, modules, noticeText, notices, resetMocks } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/notify.js', modules.notify);

import StartExperimentDialog from '@/components/experiment/StartExperimentDialog.vue';
import {
  confirmStart,
  confirmStartSelection,
  notifyStartResult,
} from '@/utils/experimentActions.js';
import { LISTED_FAILURES } from '@/utils/operations.js';
import { startResult } from '@/utils/startOptions.js';
import { buttons, dialogs, mountComponent, textOf } from './helpers/mount.js';

beforeEach(() => {
  resetMocks();
});

// attached, as a form submits only in the document
const render = (props = {}) =>
  mountComponent(StartExperimentDialog, {
    attachTo: document.body,
    props: {
      title: 'Start the Experiment',
      message: 'This will start the exp experiment.',
      ...props,
    },
  });

const CHECKBOXES = [
  'Dry run (do not call minimega)',
  'Treat minimega errors as warnings',
  "Run the apps' running stage on its schedule",
];

// each checkbox by its label: whether it is checked and disabled
const checkboxes = (wrapper) =>
  wrapper.findAll('label.checkbox').map((label) => {
    const input = label.find('input[type="checkbox"]').element;
    return [textOf(label), input.checked, input.disabled];
  });

const check = async (wrapper, label, on = true) => {
  const box = wrapper
    .findAll('label.checkbox')
    .find((el) => textOf(el) === label);
  await box.find('input').setValue(on);
};

// the number field with this label, found through its <label for>
const field = (wrapper, label) => {
  const el = wrapper.findAll('label.label').find((l) => textOf(l) === label);
  return wrapper.find(`#${el.attributes('for')}`);
};

// the error text a field's aria-describedby names, and its aria-invalid
const fieldError = (wrapper, label) => {
  const input = field(wrapper, label);
  const [errorId] = input.attributes('aria-describedby').split(' ');
  return [
    textOf(wrapper.find(`#${errorId}`)),
    input.attributes('aria-invalid'),
  ];
};

const startButton = (wrapper) =>
  wrapper.findAll('footer button').find((b) => textOf(b) === 'Start');

describe('StartExperimentDialog', () => {
  it('opens with the defaults of a plain start, every control labeled', async () => {
    const wrapper = await render();

    expect(textOf(wrapper.find('.modal-card-title'))).toBe(
      'Start the Experiment',
    );
    expect(textOf(wrapper.find('.start-message'))).toBe(
      'This will start the exp experiment.',
    );
    expect(checkboxes(wrapper)).toEqual([
      [CHECKBOXES[0], false, false],
      [CHECKBOXES[1], false, false],
      [CHECKBOXES[2], true, false],
    ]);
    for (const label of ['VLAN range minimum', 'VLAN range maximum']) {
      expect(field(wrapper, label).element.value).toBe('');
      expect(fieldError(wrapper, label)).toEqual(['', 'false']);
    }
    expect(buttons(wrapper)).toEqual(['Cancel', 'Start (submits)']);
    expect(startButton(wrapper).classes()).toContain('is-success');
  });

  it('shows the note on what a selection skips, as text', async () => {
    const wrapper = await render({ note: 'The experiment <b> is running.' });
    expect(wrapper.find('.has-text-grey-lighter').text()).toBe(
      'The experiment <b> is running.',
    );
    expect(wrapper.find('.media-content b').exists()).toBe(false);
  });

  it('greys out the schedule option for a dry run, which starts nothing', async () => {
    const wrapper = await render();
    await check(wrapper, CHECKBOXES[0]);
    expect(checkboxes(wrapper)[2]).toEqual([CHECKBOXES[2], true, true]);
  });

  it.each([
    ['empty fields', '', '', ['', ''], true],
    ['a minimum alone', '100', '', ['', ''], true],
    ['a maximum alone', '', '4094', ['', ''], true],
    ['a range', '1', '4094', ['', ''], true],
    ['zero', '0', '', ['Type an ID from 1 to 4094.', ''], false],
    [
      'an ID past the last',
      '',
      '4095',
      ['', 'Type an ID from 1 to 4094.'],
      false,
    ],
    [
      'a minimum more than the maximum',
      '200',
      '100',
      ['The minimum must not be more than the maximum.', ''],
      false,
    ],
  ])('checks %s', async (_, min, max, [minError, maxError], valid) => {
    const wrapper = await render();
    await field(wrapper, 'VLAN range minimum').setValue(min);
    await field(wrapper, 'VLAN range maximum').setValue(max);

    expect(fieldError(wrapper, 'VLAN range minimum')).toEqual([
      minError,
      minError ? 'true' : 'false',
    ]);
    expect(fieldError(wrapper, 'VLAN range maximum')).toEqual([
      maxError,
      maxError ? 'true' : 'false',
    ]);
    expect(startButton(wrapper).attributes('disabled') === undefined).toBe(
      valid,
    );

    await wrapper.find('form').trigger('submit');
    expect(wrapper.emitted('start') !== undefined).toBe(valid);
  });

  it.each([
    ['the defaults', [], {}, {}],
    ['a dry run', [CHECKBOXES[0]], {}, { dryRun: true }],
    [
      'minimega errors as warnings',
      [CHECKBOXES[1]],
      {},
      { treatMinimegaErrorsAsWarnings: true },
    ],
    [
      'no scheduled runs',
      [[CHECKBOXES[2], false]],
      {},
      { honorRunPeriodically: false },
    ],
    [
      'a dry run with the schedule unchecked',
      [[CHECKBOXES[2], false], CHECKBOXES[0]],
      {},
      { dryRun: true },
    ],
    [
      'a VLAN range',
      [],
      { min: '100', max: '200' },
      { vlanMin: 100, vlanMax: 200 },
    ],
    ['a VLAN minimum alone', [], { min: '7' }, { vlanMin: 7 }],
    [
      'every option',
      [CHECKBOXES[1], [CHECKBOXES[2], false]],
      { min: '1', max: '4094' },
      {
        treatMinimegaErrorsAsWarnings: true,
        honorRunPeriodically: false,
        vlanMin: 1,
        vlanMax: 4094,
      },
    ],
  ])(
    'gives the body of %s when Start is pressed',
    async (_, boxes, vlans, body) => {
      const wrapper = await render();
      for (const box of boxes) {
        const [label, on] = Array.isArray(box) ? box : [box, true];
        await check(wrapper, label, on);
      }
      if (vlans.min)
        await field(wrapper, 'VLAN range minimum').setValue(vlans.min);
      if (vlans.max)
        await field(wrapper, 'VLAN range maximum').setValue(vlans.max);

      startButton(wrapper).element.click();
      await flushPromises();
      expect(wrapper.emitted('start')).toEqual([[body]]);
      expect(wrapper.emitted('close')).toBeTruthy();
      expect(wrapper.emitted('cancel')).toBeUndefined();
    },
  );

  it('starts on Enter in a field, as a form submits', async () => {
    const wrapper = await render();
    const min = field(wrapper, 'VLAN range minimum');
    await min.setValue('5');
    await wrapper.find('form').trigger('submit');
    expect(wrapper.emitted('start')).toEqual([[{ vlanMin: 5 }]]);
  });

  it('cancels with Cancel, giving no body', async () => {
    const wrapper = await render();
    await wrapper.findAll('footer button')[0].trigger('click');
    expect(wrapper.emitted('cancel')).toEqual([[]]);
    expect(wrapper.emitted('close')).toEqual([[]]);
    expect(wrapper.emitted('start')).toBeUndefined();
  });

  it('gives a body only for a valid range, for any two IDs', async () => {
    const wrapper = await render();
    await fc.assert(
      fc.asyncProperty(
        fc.integer({ min: -5, max: 4100 }),
        fc.integer({ min: -5, max: 4100 }),
        async (min, max) => {
          await field(wrapper, 'VLAN range minimum').setValue(String(min));
          await field(wrapper, 'VLAN range maximum').setValue(String(max));
          const valid =
            min >= 1 && max <= 4094 && max >= 1 && min <= 4094 && min <= max;
          expect(
            startButton(wrapper).attributes('disabled') === undefined,
          ).toBe(valid);
        },
      ),
      { numRuns: 20 },
    );
  });
});

describe('confirmStart', () => {
  // the $buefy of a mounted component, which opens the programmatic modal
  const buefy = async () =>
    (await mountComponent({ render: () => null })).vm.$buefy;
  const shown = () => dialogs().at(-1);

  it('opens the dialog with the look of a confirm dialog and resolves to the body', async () => {
    const answer = confirmStart(await buefy(), 'a<b>');
    await flushPromises();
    const dialog = shown();
    expect(textOf(dialog.querySelector('.modal-card-title'))).toBe(
      'Start the Experiment',
    );
    expect(dialog.querySelector('.start-message').innerHTML).toBe(
      'This will start the a&lt;b&gt; experiment.',
    );
    expect(dialog.getAttribute('role')).toBe('dialog');
    expect(dialog.getAttribute('aria-labelledby')).toBeTruthy();

    const dry = dialog.querySelector('input[type="checkbox"]');
    dry.checked = true;
    dry.dispatchEvent(new Event('change'));
    await flushPromises();
    dialog.querySelector('footer .button:last-child').click();
    expect(await answer).toEqual({ dryRun: true });
  });

  it('resolves to {} for the defaults, so the request has no body', async () => {
    const answer = confirmStart(await buefy(), 'exp');
    await flushPromises();
    shown().querySelector('footer .button:last-child').click();
    expect(await answer).toEqual({});
  });

  it.each([
    ['Cancel', () => shown().querySelector('footer .button').click()],
    [
      'Escape',
      () =>
        document.dispatchEvent(new KeyboardEvent('keyup', { key: 'Escape' })),
    ],
  ])('resolves to null on %s', async (_, act) => {
    const answer = confirmStart(await buefy(), 'exp');
    await flushPromises();
    act();
    expect(await answer).toBeNull();
  });

  it('names a selection, and what it skips', async () => {
    const answer = confirmStartSelection(
      await buefy(),
      ['a', 'b'],
      [{ reason: 'the experiment is already running', names: ['c'] }],
    );
    await flushPromises();
    const dialog = shown();
    expect(textOf(dialog.querySelector('.modal-card-title'))).toBe(
      'Start the Experiments',
    );
    expect(textOf(dialog.querySelector('.start-message'))).toBe(
      'This will start the experiments a and b.',
    );
    expect(textOf(dialog.querySelector('.has-text-grey-lighter'))).toBe(
      'The c experiment is already running.',
    );
    dialog.querySelector('footer .button:last-child').click();
    expect(await answer).toEqual({});
  });
});

describe('notifyStartResult', () => {
  const exp = { name: 'exp', running: false };
  const script = '/phenix/experiments/exp/mm_files/exp.mm';
  // the notice's message drawn, to read it as a user does
  const drawn = () => mount({ render: () => lastNotice().message });

  it('shows nothing for a plain start', () => {
    notifyStartResult('exp', startResult(exp));
    notifyStartResult('exp', startResult({ experiment: exp, warnings: [] }));
    expect(notices.open).not.toHaveBeenCalled();
  });

  it('tells of a dry run: no minimega, still stopped, and the script', () => {
    notifyStartResult(
      'exp',
      startResult({
        experiment: exp,
        dryRun: true,
        warnings: [],
        mmScript: script,
      }),
    );
    expect(lastNotice()).toMatchObject({ type: 'is-info', indefinite: true });
    const message = drawn();
    expect(message.attributes('role')).toBe('status');
    expect(message.find('.error-title').text()).toBe(
      'Dry run of the exp experiment',
    );
    expect(message.findAll('p').map((p) => p.text())).toEqual([
      'phēnix did not call minimega. The experiment stays stopped.',
      `minimega script: ${script}`,
    ]);
    expect(message.findAll('.error-step')).toHaveLength(0);
  });

  it('lists the warnings of a dry run, as a warning', () => {
    notifyStartResult(
      'exp',
      startResult({
        experiment: exp,
        dryRun: true,
        warnings: ['no image for vm1'],
        mmScript: script,
      }),
    );
    expect(lastNotice().type).toBe('is-warning');
    const message = drawn();
    expect(message.attributes('role')).toBe('alert');
    expect(message.findAll('p').at(-1).text()).toBe('1 warning:');
    expect(message.findAll('.error-step').map((el) => el.text())).toEqual([
      'no image for vm1',
    ]);
  });

  it('lists the minimega errors a start took as warnings, escaped', () => {
    notifyStartResult(
      'exp',
      startResult({
        experiment: exp,
        warnings: ['vm <b>1</b> failed', 'tap failed'],
      }),
    );
    expect(lastNotice().type).toBe('is-warning');
    expect(noticeText()).toContain(
      'Started the exp experiment with 2 warnings',
    );
    const message = drawn();
    expect(message.findAll('.error-step').map((el) => el.text())).toEqual([
      'vm <b>1</b> failed',
      'tap failed',
    ]);
    expect(message.find('.error-step b').exists()).toBe(false);
  });

  it('lists at most LISTED_FAILURES warnings and counts the rest', () => {
    const warnings = Array.from(
      { length: LISTED_FAILURES + 3 },
      (_, i) => `w${i}`,
    );
    notifyStartResult('exp', startResult({ experiment: exp, warnings }));
    const steps = drawn().findAll('.error-step');
    expect(steps).toHaveLength(LISTED_FAILURES + 1);
    expect(steps.at(-1).text()).toBe('and 3 more');
  });
});
