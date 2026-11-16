// The stop and restart dialogs of an experiment (StopExperimentDialog.vue,
// RestartExperimentDialog.vue): their options, the body each gives, how
// confirmStop and confirmRestart open them, the notices of the warnings in a
// stop or restart answer, and the offer to force a restart.
import { flushPromises, mount } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { lastNotice, modules, notices, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/notify.js', modules.notify);

import RestartExperimentDialog from '@/components/experiment/RestartExperimentDialog.vue';
import StopExperimentDialog from '@/components/experiment/StopExperimentDialog.vue';
import {
  confirmRestart,
  confirmRestartSelection,
  confirmStop,
  confirmStopSelection,
  notifyRestartResult,
  notifyStopResult,
  offerForcedRestart,
} from '@/utils/experimentActions.js';
import { buttons, dialogs, mountComponent, textOf } from './helpers/mount.js';

beforeEach(() => {
  resetMocks();
});

const KEEP = 'Keep the injection snapshots';
const FORCE = 'Force the stop';
const ERRORS = 'Treat minimega errors as warnings';
const SCHEDULE = "Run the apps' running stage on its schedule";

// attached, as a form submits only in the document
const render = (component, props = {}) =>
  mountComponent(component, {
    attachTo: document.body,
    props: { title: 'T', message: 'M', ...props },
  });

// Each checkbox by its label: whether it is checked, and the text of the
// help line its input names, if any.
const checkboxes = (root) =>
  [...root.querySelectorAll('label.checkbox')].map((label) => {
    const input = label.querySelector('input[type="checkbox"]');
    const help = input.getAttribute('aria-describedby');
    return [
      textOf(label),
      input.checked,
      help ? textOf(document.getElementById(help)) : null,
    ];
  });

const check = async (root, label, on = true) => {
  const box = [...root.querySelectorAll('label.checkbox')].find(
    (el) => textOf(el) === label,
  );
  const input = box.querySelector('input');
  input.checked = on;
  input.dispatchEvent(new Event('change'));
  await flushPromises();
};

const confirmButton = (root) => root.querySelector('footer .button:last-child');

const STOP_BOXES = [
  [KEEP, false, 'phēnix deletes them after a stop otherwise.'],
  [
    FORCE,
    false,
    'Marks the experiment stopped even if minimega cannot kill its VMs. ' +
      'You must then clear those VMs in minimega by hand.',
  ],
];

describe('StopExperimentDialog', () => {
  it('opens with both options off, each naming its help line', async () => {
    const wrapper = await render(StopExperimentDialog);
    expect(checkboxes(wrapper.element)).toEqual(STOP_BOXES);
    expect(buttons(wrapper)).toEqual(['Cancel', 'Stop (submits)']);
    expect(confirmButton(wrapper.element).classList).toContain('is-danger');
  });

  it.each([
    ['no option', [], {}],
    ['the snapshots kept', [KEEP], { keepInjectionSnapshots: true }],
    ['a forced stop', [FORCE], { force: true }],
    [
      'both options',
      [KEEP, FORCE],
      { keepInjectionSnapshots: true, force: true },
    ],
    ['an option checked, then cleared', [KEEP, [KEEP, false]], {}],
  ])('gives the options of %s', async (_, boxes, body) => {
    const wrapper = await render(StopExperimentDialog);
    for (const box of boxes) await check(wrapper.element, ...[box].flat());

    confirmButton(wrapper.element).click();
    await flushPromises();
    expect(wrapper.emitted('stop')).toEqual([[body]]);
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('cancels with Cancel, giving no options', async () => {
    const wrapper = await render(StopExperimentDialog);
    await wrapper.findAll('footer button')[0].trigger('click');
    expect(wrapper.emitted('cancel')).toEqual([[]]);
    expect(wrapper.emitted('stop')).toBeUndefined();
  });
});

describe('the keys of the stop options', () => {
  // presses `key` on the input of the checkbox labeled `label`, as a
  // keyboard user does; whether the browser may act on the key itself
  const press = async (root, label, key) => {
    const input = [...root.querySelectorAll('label.checkbox')]
      .find((el) => textOf(el) === label)
      .querySelector('input');
    input.focus();
    const event = new KeyboardEvent('keydown', {
      key,
      bubbles: true,
      cancelable: true,
    });
    input.dispatchEvent(event);
    await flushPromises();
    return { checked: input.checked, defaultPrevented: event.defaultPrevented };
  };

  it.each([
    [
      'stop dialog',
      KEEP,
      StopExperimentDialog,
      'stop',
      'keepInjectionSnapshots',
    ],
    ['stop dialog', FORCE, StopExperimentDialog, 'stop', 'force'],
    [
      'restart dialog',
      KEEP,
      RestartExperimentDialog,
      'restart',
      'keepInjectionSnapshots',
    ],
    ['restart dialog', FORCE, RestartExperimentDialog, 'restart', 'force'],
  ])(
    'toggle with Enter, which does not confirm, and with Space once (%s, %s)',
    async (_, label, component, event, key) => {
      const wrapper = await render(component);
      const root = wrapper.element;

      expect(await press(root, label, 'Enter')).toEqual({
        checked: true,
        defaultPrevented: true,
      });
      expect(wrapper.emitted(event)).toBeUndefined();
      expect(await press(root, label, 'Enter')).toEqual({
        checked: false,
        defaultPrevented: true,
      });
      expect(await press(root, label, ' ')).toEqual({
        checked: true,
        defaultPrevented: true,
      });
      expect(wrapper.emitted(event)).toBeUndefined();

      confirmButton(root).click();
      await flushPromises();
      expect(wrapper.emitted(event)).toEqual([[{ [key]: true }]]);
    },
  );
});

describe('RestartExperimentDialog', () => {
  it('shows the stop options, then the start options without the dry run', async () => {
    const wrapper = await render(RestartExperimentDialog);
    expect(wrapper.findAll('legend').map((legend) => textOf(legend))).toEqual([
      'Stop options',
      'Start options',
      'VLAN range',
    ]);
    expect(checkboxes(wrapper.element)).toEqual([
      ...STOP_BOXES,
      [ERRORS, false, null],
      [SCHEDULE, true, null],
    ]);
    expect(buttons(wrapper)).toEqual(['Cancel', 'Restart (submits)']);
  });

  it.each([
    ['the defaults', [], {}, {}],
    [
      'every option',
      [KEEP, FORCE, ERRORS, [SCHEDULE, false]],
      { vlanMin: '10', vlanMax: '20' },
      {
        keepInjectionSnapshots: true,
        force: true,
        treatMinimegaErrorsAsWarnings: true,
        honorRunPeriodically: false,
        vlanMin: 10,
        vlanMax: 20,
      },
    ],
  ])('gives the body of %s', async (_, boxes, vlans, body) => {
    const wrapper = await render(RestartExperimentDialog);
    for (const box of boxes) await check(wrapper.element, ...[box].flat());
    for (const [key, value] of Object.entries(vlans)) {
      const label = key == 'vlanMin' ? 'minimum' : 'maximum';
      const field = wrapper
        .findAll('label.label')
        .find((l) => textOf(l) === `VLAN range ${label}`);
      await wrapper.find(`#${field.attributes('for')}`).setValue(value);
    }

    confirmButton(wrapper.element).click();
    await flushPromises();
    expect(wrapper.emitted('restart')).toEqual([[body]]);
  });

  it('gives no body for a VLAN range that is not valid', async () => {
    const wrapper = await render(RestartExperimentDialog);
    const field = wrapper
      .findAll('label.label')
      .find((l) => textOf(l) === 'VLAN range minimum');
    await wrapper.find(`#${field.attributes('for')}`).setValue('0');

    expect(confirmButton(wrapper.element).disabled).toBe(true);
    await wrapper.find('form').trigger('submit');
    expect(wrapper.emitted('restart')).toBeUndefined();
  });
});

describe('confirmStop and confirmRestart', () => {
  // the $buefy of a mounted component, which opens the programmatic modal
  const buefy = async () =>
    (await mountComponent({ render: () => null })).vm.$buefy;
  const shown = () => dialogs().at(-1);
  // what the newest dialog says
  const said = () => {
    const dialog = shown();
    return {
      title: textOf(dialog.querySelector('.modal-card-title')),
      message: dialog.querySelector('.dialog-message').innerHTML,
      note: dialog.querySelector('.has-text-grey-lighter')?.textContent ?? '',
      confirm: textOf(confirmButton(dialog)),
    };
  };

  it.each([
    [
      'a stop',
      (b) => confirmStop(b, 'a<b>'),
      {
        title: 'Stop the Experiment',
        message: 'This will stop the a&lt;b&gt; experiment.',
        note: '',
        confirm: 'Stop',
      },
    ],
    [
      'the stop of a selection',
      (b) =>
        confirmStopSelection(
          b,
          ['a', 'b'],
          [{ reason: 'the experiment is not running', names: ['c'] }],
        ),
      {
        title: 'Stop the Experiments',
        message: 'This will stop the experiments a and b.',
        note: 'The c experiment is not running.',
        confirm: 'Stop',
      },
    ],
    [
      'a restart',
      (b) => confirmRestart(b, 'a<b>'),
      {
        title: 'Restart the Experiment',
        message: 'This will stop the a&lt;b&gt; experiment and start it again.',
        note: '',
        confirm: 'Restart',
      },
    ],
    [
      'the restart of a selection',
      (b) => confirmRestartSelection(b, ['a', 'b']),
      {
        title: 'Restart the Experiments',
        message: 'This will stop the experiments a and b and start them again.',
        note: '',
        confirm: 'Restart',
      },
    ],
  ])(
    'ask for %s as text, with the look of a confirm dialog',
    async (_, open, want) => {
      const answer = open(await buefy());
      await flushPromises();
      expect(said()).toEqual(want);
      expect(shown().getAttribute('role')).toBe('dialog');
      confirmButton(shown()).click();
      expect(await answer).toEqual({});
    },
  );

  it('opens the stop dialog with both options off each time', async () => {
    const b = await buefy();
    const first = confirmStop(b, 'exp');
    await flushPromises();
    await check(shown(), KEEP);
    await check(shown(), FORCE);
    confirmButton(shown()).click();
    expect(await first).toEqual({ keepInjectionSnapshots: true, force: true });

    const second = confirmStop(b, 'exp');
    await flushPromises();
    expect(checkboxes(shown())).toEqual(STOP_BOXES);
    confirmButton(shown()).click();
    expect(await second).toEqual({});
  });

  it.each([
    ['confirmStop', confirmStop],
    ['confirmRestart', confirmRestart],
  ])('%s resolves to null on Cancel', async (_, open) => {
    const answer = open(await buefy(), 'exp');
    await flushPromises();
    shown().querySelector('footer .button').click();
    expect(await answer).toBeNull();
  });

  it('offers to force a restart, then sends it', async () => {
    const onForce = vi.fn();
    offerForcedRestart(await buefy(), ['a<b>'], onForce);
    await flushPromises();
    const dialog = shown();
    expect(textOf(dialog.querySelector('header'))).toBe('Force the Restart');
    expect(textOf(dialog)).toContain(
      'minimega could not kill the VMs of the a<b> experiment',
    );
    expect(textOf(confirmButton(dialog))).toBe('Force Restart');
    confirmButton(dialog).click();
    await flushPromises();
    expect(onForce).toHaveBeenCalledTimes(1);
  });
});

describe('the warnings of a stop or a restart', () => {
  const exp = { name: 'exp', running: false };
  const leftover = 'minimega could not kill the VMs; clear them by hand';
  // the notice's message drawn, to read it as a user does
  const drawn = () => mount({ render: () => lastNotice().message });

  it.each([
    ['a stop answered with the experiment', notifyStopResult, exp],
    [
      'a forced stop that killed the VMs',
      notifyStopResult,
      { experiment: exp, warnings: [] },
    ],
    ['a stop with no body', notifyStopResult, undefined],
    ['a restart answered with the experiment', notifyRestartResult, exp],
  ])('shows nothing for %s', (_, notify, data) => {
    notify('exp', data);
    expect(notices.open).not.toHaveBeenCalled();
  });

  it.each([
    ['Stopped', notifyStopResult],
    ['Restarted', notifyRestartResult],
  ])('names the experiment and lists the warnings: %s', (verb, notify) => {
    notify('exp', { experiment: exp, warnings: [leftover] });
    expect(lastNotice()).toMatchObject({
      type: 'is-warning',
      indefinite: true,
    });
    const message = drawn();
    expect(message.attributes('role')).toBe('alert');
    expect(textOf(message.find('.error-title'))).toBe(
      `${verb} the exp experiment with 1 warning`,
    );
    expect(message.findAll('.error-step').map((el) => el.text())).toEqual([
      leftover,
    ]);
  });
});
