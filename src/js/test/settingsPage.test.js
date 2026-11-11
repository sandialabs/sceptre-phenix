// The Settings page: loads the settings into a form, offers reset and save
// only after a valid change, and keeps unsaved edits when the settings reload.
import { isDeepStrictEqual } from 'node:util';
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);

import { clearPageCache } from '@/utils/pageCache.js';
import { pageStatus } from '@/utils/pageLoader.js';
import Settings, {
  overriddenBy,
  readConnectionsError,
  refusalMessage,
  timeoutErrors,
} from '@/views/Settings.vue';
import { never } from './helpers/async.js';
import { httpError } from './helpers/fixtures.js';
import { buttons, mountPage, textOf, toasts } from './helpers/mount.js';
import { componentDeclarations, contrast } from './helpers/css.js';

const settings = () => ({
  password_settings: {
    lowercase_req: true,
    uppercase_req: false,
    number_req: false,
    symbol_req: false,
    min_length: 8,
  },
  timeout_settings: { enabled: false, timeout_min: 30, warning_min: 3 },
  logging_settings: {
    max_file_size: 100,
    max_file_rotations: 3,
    max_file_age: 365,
  },
});

const UNCHANGED = [
  'Reset Form (disabled)',
  'Save Changes (submits) (disabled)',
];
const CHANGED = ['Reset Form', 'Save Changes (submits)'];
const INVALID = ['Reset Form', 'Save Changes (submits) (disabled)'];

const mountSettings = () => mountPage(Settings, { route: '/settings' });

// the page once the server has sent it these settings
function open(data = settings()) {
  axios.get.mockResolvedValueOnce({ data });
  return mountSettings();
}

// the switches that are on
const on = (page) =>
  page
    .findAll('label.switch')
    .filter((sw) => sw.find('input').element.checked)
    .map(textOf);

const control = (page, id) => page.find(`[aria-labelledby="${id}"]`);
const minLength = (page) => control(page, 'settings-password-min-length');

// the switch with this text
const switchOf = (page, text) =>
  page
    .findAll('label.switch')
    .find((sw) => textOf(sw) === text)
    .find('input');

// each setting's switch, by its text, and number box, by its label's id
const SWITCHES = {
  'Require a lowercase letter': ['password_settings', 'lowercase_req'],
  'Require an uppercase letter': ['password_settings', 'uppercase_req'],
  'Require a number': ['password_settings', 'number_req'],
  'Require a symbol': ['password_settings', 'symbol_req'],
  'Log out users after period of inactivity': ['timeout_settings', 'enabled'],
};
const NUMBERS = {
  'settings-password-min-length': ['password_settings', 'min_length'],
  'settings-idle-timeout': ['timeout_settings', 'timeout_min'],
  'settings-idle-warning': ['timeout_settings', 'warning_min'],
  'settings-log-max-size': ['logging_settings', 'max_file_size'],
  'settings-log-max-rotations': ['logging_settings', 'max_file_rotations'],
  'settings-log-max-age': ['logging_settings', 'max_file_age'],
};

// the settings the form shows
function form(page) {
  const shown = {
    password_settings: {},
    timeout_settings: {},
    logging_settings: {},
  };
  for (const [text, [group, key]] of Object.entries(SWITCHES)) {
    shown[group][key] = switchOf(page, text).element.checked;
  }
  for (const [id, [group, key]] of Object.entries(NUMBERS)) {
    shown[group][key] = Number(control(page, id).element.value);
  }
  return shown;
}

const button = (page, text) =>
  page.findAll('button').find((b) => textOf(b) === text);

beforeEach(() => {
  clearPageCache();
  resetMocks({ store: {} });
});

describe('the Settings page', () => {
  it("says it is loading, then shows the server's settings", async () => {
    axios.get.mockImplementationOnce(never);
    const waiting = await mountSettings();
    expect(axios.get.mock.calls[0][0]).toBe('settings');
    expect(textOf(waiting.find('h1'))).toBe('Settings');
    expect(textOf(waiting.find('p'))).toBe('Loading settings…');
    expect(waiting.find('form').exists()).toBe(false);

    // another visit, which would otherwise join the load still waiting
    clearPageCache();
    const page = await open();
    expect(on(page)).toEqual(['Require a lowercase letter']);
    expect(buttons(page)).toEqual(UNCHANGED);
  });

  it('says so when the settings could not be loaded', async () => {
    const err = new Error('forbidden');
    axios.get.mockRejectedValueOnce(err);
    const page = await mountSettings();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(pageStatus.failed).toBe(true);
    expect(textOf(page.find('h1'))).toBe('Settings');
    expect(textOf(page.find('p'))).toBe('Could not load settings');
  });

  it('has one h1 and its section headings at the next level', async () => {
    const page = await open();
    expect(page.findAll('h1').map(textOf)).toEqual(['Settings']);
    expect(page.findAll('h2').map(textOf)).toEqual([
      'Password Settings',
      'Timeout Settings',
      'File Logging Settings',
    ]);
    expect(page.find('h3').exists()).toBe(false);
  });

  it('puts back the settings last loaded on Reset Form', async () => {
    const page = await open();
    await switchOf(page, 'Log out users after period of inactivity').setValue(
      true,
    );
    expect(on(page)).toEqual([
      'Require a lowercase letter',
      'Log out users after period of inactivity',
    ]);
    expect(buttons(page)).toEqual(CHANGED);

    await button(page, 'Reset Form').trigger('click');
    expect(form(page)).toEqual(settings());
    expect(on(page)).toEqual(['Require a lowercase letter']);
    expect(buttons(page)).toEqual(UNCHANGED);
  });

  it('saves the changes once, however often the form is submitted', async () => {
    const answer = Promise.withResolvers();
    axios.post.mockReturnValue(answer.promise);
    const page = await open();
    const form = page.find('form');

    // a submit with nothing changed sends nothing
    await form.trigger('submit');
    expect(axios.post).not.toHaveBeenCalled();

    await minLength(page).setValue('12');
    await form.trigger('submit');
    await form.trigger('submit');
    const edited = settings();
    edited.password_settings.min_length = 12;
    expect(axios.post.mock.calls).toEqual([
      ['settings', edited, { timeout: 0 }],
    ]);
    expect(buttons(page)).toEqual([
      'Reset Form',
      'Save Changes (submits) (spinning)',
    ]);

    answer.resolve({});
    await flushPromises();
    expect(toasts().map((t) => t.text)).toEqual(['Settings updated']);
    expect(buttons(page)).toEqual(UNCHANGED);

    // the page opens with the saved settings next time, before they load
    axios.get.mockImplementationOnce(never);
    const next = await mountSettings();
    expect(minLength(next).element.value).toBe('12');
  });

  it('saves exactly the edits made to any settings', async () => {
    const minutes = fc.integer({ min: 1, max: 240 }).map((n) => n / 2);
    const FIELDS = {
      password_settings: {
        lowercase_req: fc.boolean(),
        uppercase_req: fc.boolean(),
        number_req: fc.boolean(),
        symbol_req: fc.boolean(),
        min_length: fc.integer({ min: 4, max: 32 }),
      },
      timeout_settings: {
        enabled: fc.boolean(),
        timeout_min: minutes,
        warning_min: minutes,
      },
      logging_settings: {
        max_file_size: fc.nat(1000),
        max_file_rotations: fc.nat(100),
        max_file_age: fc.nat(1000),
      },
    };
    // settings, each field drawn from pick(arb, group, key)
    const fieldsArb = (pick) =>
      fc.record(
        Object.fromEntries(
          Object.entries(FIELDS).map(([group, fields]) => [
            group,
            fc.record(
              Object.fromEntries(
                Object.entries(fields).map(([key, arb]) => [
                  key,
                  pick(arb, group, key),
                ]),
              ),
              { noNullPrototype: true },
            ),
          ]),
        ),
        { noNullPrototype: true },
      );
    // the settings loaded, then the same or other values typed over them
    const loadedAndEdited = fieldsArb((arb) => arb).chain((loaded) =>
      fc.tuple(
        fc.constant(loaded),
        fc.oneof(
          fc.constant(structuredClone(loaded)),
          fieldsArb((arb, g, k) => fc.oneof(fc.constant(loaded[g][k]), arb)),
        ),
      ),
    );

    await fc.assert(
      fc.asyncProperty(loadedAndEdited, async ([loaded, edited]) => {
        clearPageCache();
        axios.post.mockReset().mockResolvedValue({});
        const page = await open(structuredClone(loaded));
        expect(form(page)).toEqual(loaded);

        for (const [text, [group, key]] of Object.entries(SWITCHES)) {
          await switchOf(page, text).setValue(edited[group][key]);
        }
        // the idle times can be changed only while idle logout is on
        const want = structuredClone(edited);
        if (!edited.timeout_settings.enabled) {
          want.timeout_settings = {
            ...loaded.timeout_settings,
            enabled: false,
          };
        }
        for (const [id, [group, key]] of Object.entries(NUMBERS)) {
          const input = control(page, id);
          expect(input.element.disabled).toBe(
            group === 'timeout_settings' && !edited.timeout_settings.enabled,
          );
          if (!input.element.disabled) {
            await input.setValue(String(edited[group][key]));
          }
        }
        expect(form(page)).toEqual(want);

        const changed = !isDeepStrictEqual(want, loaded);
        // an idle time the server would reject is shown and not saved
        const errors = timeoutErrors(want.timeout_settings);
        const valid = !errors.timeout && !errors.warning;
        expect(page.find('#settings-idle-warning-error').exists()).toBe(
          Boolean(errors.warning),
        );
        expect(buttons(page)).toEqual(
          changed ? (valid ? CHANGED : INVALID) : UNCHANGED,
        );
        await page.find('form').trigger('submit');
        await flushPromises();
        expect(axios.post.mock.calls.map(([, sent]) => sent)).toEqual(
          changed && valid ? [want] : [],
        );
        page.unmount();
      }),
      { numRuns: 15 },
    );
  });

  it('shows an inline error and blocks Save for a warning as long as the idle time', async () => {
    const data = settings();
    data.timeout_settings = { enabled: true, timeout_min: 10, warning_min: 2 };
    const page = await open(data);
    expect(page.find('#settings-idle-warning-error').exists()).toBe(false);

    const warning = control(page, 'settings-idle-warning');
    await warning.setValue('10');
    expect(warning.attributes('aria-invalid')).toBe('true');
    expect(warning.attributes('aria-describedby')).toBe(
      'settings-idle-warning-error',
    );
    expect(textOf(page.find('#settings-idle-warning-error'))).toBe(
      'Enter a warning time of less than the idle time (10 minutes).',
    );
    expect(buttons(page)).toEqual(INVALID);

    await page.find('form').trigger('submit');
    await flushPromises();
    expect(axios.post).not.toHaveBeenCalled();

    // a shorter warning clears the error and saves
    axios.post.mockResolvedValue({});
    await warning.setValue('2.5');
    expect(warning.attributes('aria-invalid')).toBeUndefined();
    expect(page.find('#settings-idle-warning-error').exists()).toBe(false);
    await page.find('form').trigger('submit');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledOnce();
  });

  it('shows an inline error for an idle time under half a minute', async () => {
    const data = settings();
    data.timeout_settings = { enabled: true, timeout_min: 0, warning_min: 0 };
    const page = await open(data);

    const timeout = control(page, 'settings-idle-timeout');
    expect(timeout.attributes('aria-invalid')).toBe('true');
    expect(timeout.attributes('aria-describedby')).toBe(
      'settings-idle-timeout-error',
    );
    expect(textOf(page.find('#settings-idle-timeout-error'))).toBe(
      'Enter an idle time of 0.5 minutes or more.',
    );
  });

  it('does not check the idle times while idle logout is off', async () => {
    axios.post.mockResolvedValue({});
    const data = settings();
    data.timeout_settings = { enabled: false, timeout_min: 0, warning_min: 5 };
    const page = await open(data);
    expect(page.find('#settings-idle-timeout-error').exists()).toBe(false);
    expect(page.find('#settings-idle-warning-error').exists()).toBe(false);

    await switchOf(page, 'Require a number').setValue(true);
    expect(buttons(page)).toEqual(CHANGED);
    await page.find('form').trigger('submit');
    await flushPromises();
    expect(axios.post).toHaveBeenCalledOnce();
  });

  it('keeps the edits and reports a save that fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const page = await open();
    await control(page, 'settings-log-max-age').setValue('30');

    await page.find('form').trigger('submit');
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(control(page, 'settings-log-max-age').element.value).toBe('30');
    expect(buttons(page)).toEqual(CHANGED);
  });

  it('keeps unsaved edits when the settings load again', async () => {
    const page = await open();
    await switchOf(page, 'Require a symbol').setValue(true);

    const changed = settings();
    changed.timeout_settings.timeout_min = 60;
    axios.get.mockResolvedValueOnce({ data: changed });
    await pageStatus.refresh();
    await flushPromises();

    expect(on(page)).toContain('Require a symbol');
    expect(control(page, 'settings-idle-timeout').element.value).toBe('30');
    await button(page, 'Reset Form').trigger('click');
    expect(on(page)).toEqual(['Require a lowercase letter']);
    expect(control(page, 'settings-idle-timeout').element.value).toBe('60');
    expect(buttons(page)).toEqual(UNCHANGED);
  });

  it('loads the log file limits into their fields and names the runtime setting of each', async () => {
    const page = await open();
    const logging = {
      'settings-log-max-size': [
        100,
        'Changes log.system.max-size. 0 is 100 MiB.',
      ],
      'settings-log-max-rotations': [3, 'Changes log.system.max-backups.'],
      'settings-log-max-age': [365, 'Changes log.system.max-age, in days.'],
    };
    expect(textOf(page.find('#settings-log-help'))).toBe(
      'These fields change runtime settings of phenix ui. A change applies at once, and phenix writes it to the config file.',
    );
    for (const [id, [value, help]] of Object.entries(logging)) {
      const input = control(page, id);
      expect(Number(input.element.value)).toBe(value);
      expect(input.attributes('aria-describedby')).toBe(
        `${id}-help settings-log-help`,
      );
      expect(textOf(page.find(`#${id}-help`))).toBe(help);
    }
  });

  it('sends the edited log file limits with the other settings', async () => {
    axios.post.mockResolvedValue({});
    const page = await open();
    await control(page, 'settings-log-max-size').setValue('50');
    await control(page, 'settings-log-max-rotations').setValue('0');

    await page.find('form').trigger('submit');
    await flushPromises();
    const edited = settings();
    edited.logging_settings.max_file_size = 50;
    edited.logging_settings.max_file_rotations = 0;
    expect(axios.post.mock.calls).toEqual([
      ['settings', edited, { timeout: 0 }],
    ]);
    expect(toasts().map((t) => t.text)).toEqual(['Settings updated']);
    expect(page.find('#settings-save-error').exists()).toBe(false);
  });

  it.each([
    [
      400,
      'error updating logging settings: log file limit is less than 0: log.system.max-size must be 0 or more, not -1',
    ],
    [
      409,
      'error updating logging settings: a flag or an environment variable sets this log file limit: the PHENIX_LOG_SYSTEM_MAX_AGE environment variable of phenix ui sets log.system.max-age. Remove the variable and restart phenix ui. Then change the value here',
    ],
  ])(
    "shows the server's message for a %d refusal and keeps the edits",
    async (status, message) => {
      axios.post.mockRejectedValueOnce(
        httpError(status, { message, code: 'conflict' }),
      );
      const page = await open();
      await control(page, 'settings-log-max-age').setValue('30');

      await page.find('form').trigger('submit');
      await flushPromises();
      const error = page.find('#settings-save-error');
      expect(error.attributes('role')).toBe('alert');
      expect(textOf(error)).toBe(
        `The server did not save the settings: ${message}`,
      );
      expect(notify.error).not.toHaveBeenCalled();
      expect(control(page, 'settings-log-max-age').element.value).toBe('30');
      expect(buttons(page)).toEqual(CHANGED);

      // the next save that works clears the message
      axios.post.mockResolvedValueOnce({});
      await page.find('form').trigger('submit');
      await flushPromises();
      expect(page.find('#settings-save-error').exists()).toBe(false);
      expect(buttons(page)).toEqual(UNCHANGED);
    },
  );

  it('reports any other failed save in a notification', async () => {
    const err = httpError(500, { message: 'Error updating settings' });
    axios.post.mockRejectedValueOnce(err);
    const page = await open();
    await control(page, 'settings-log-max-age').setValue('30');

    await page.find('form').trigger('submit');
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(page.find('#settings-save-error').exists()).toBe(false);
  });

  it('clears the refusal on Reset Form', async () => {
    axios.post.mockRejectedValueOnce(
      httpError(409, { message: 'phenix ui has no config file' }),
    );
    const page = await open();
    await control(page, 'settings-log-max-size').setValue('10');
    await page.find('form').trigger('submit');
    await flushPromises();
    expect(page.find('#settings-save-error').exists()).toBe(true);

    await button(page, 'Reset Form').trigger('click');
    expect(page.find('#settings-save-error').exists()).toBe(false);
    expect(form(page)).toEqual(settings());
  });

  // a compact dark box before each label, not a wide white one after it
  it('draws each number as a small dark box ahead of its label', async () => {
    const page = await open();
    for (const id of Object.keys(NUMBERS)) {
      const field = control(page, id).element.closest('.setting-number');
      expect([...field.children].map((el) => el.tagName)).toEqual([
        'DIV',
        'SPAN',
      ]);
      expect(field.lastElementChild.id).toBe(id);
    }

    const style = (selector) =>
      componentDeclarations('src/views/Settings.vue', selector);
    expect(style('.setting-number')).toMatchObject({ display: 'flex' });
    expect(style('.setting-number :deep(.b-numberinput)')).toMatchObject({
      flex: 'none',
      width: '5.5em',
    });
    const input = style('.setting-number :deep(.input)');
    const formBackground = '#484848';
    expect(
      contrast(input.color, input['background-color']),
    ).toBeGreaterThanOrEqual(4.5);
    expect(
      contrast(input['border-color'], formBackground),
    ).toBeGreaterThanOrEqual(3);
  });
});

describe('refusalMessage', () => {
  it.each([
    [httpError(400, { message: 'bad' }), 'bad'],
    [httpError(409, '{"message":"in use"}'), 'in use'],
    [httpError(409, { message: '' }), null],
    [httpError(409, {}), null],
    [httpError(409, 'not JSON'), null],
    [httpError(500, { message: 'broken' }), null],
    [httpError(403, { message: 'forbidden' }), null],
    [new Error('offline'), null],
    [null, null],
  ])('%o gives %o', (err, want) => {
    expect(refusalMessage(err)).toBe(want);
  });
});

describe('timeoutErrors', () => {
  const IDLE = 'Enter an idle time of 0.5 minutes or more.';
  const NEGATIVE = 'Enter a warning time of 0 minutes or more.';
  const shorter = (min) =>
    `Enter a warning time of less than the idle time (${min} minutes).`;

  it.each([
    [{ enabled: true, timeout_min: 30, warning_min: 3 }, null, null],
    [{ enabled: true, timeout_min: 30, warning_min: 0 }, null, null],
    [{ enabled: true, timeout_min: 0.5, warning_min: 0 }, null, null],
    [{ enabled: true, timeout_min: 1, warning_min: 0.5 }, null, null],
    [{ enabled: false, timeout_min: 0, warning_min: 50 }, null, null],
    [{ enabled: false, timeout_min: null, warning_min: -1 }, null, null],
    [{ enabled: true, timeout_min: 10, warning_min: 10 }, null, shorter(10)],
    [{ enabled: true, timeout_min: 10, warning_min: 15 }, null, shorter(10)],
    [{ enabled: true, timeout_min: 0, warning_min: 0 }, IDLE, null],
    [{ enabled: true, timeout_min: 0.4, warning_min: 0 }, IDLE, null],
    [{ enabled: true, timeout_min: null, warning_min: 0 }, IDLE, null],
    [{ enabled: true, timeout_min: 0.5, warning_min: 0.5 }, null, shorter(0.5)],
    [{ enabled: true, timeout_min: 30, warning_min: -1 }, null, NEGATIVE],
  ])('%o', (idle, timeout, warning) => {
    expect(timeoutErrors(idle)).toEqual({ timeout, warning });
  });

  it('accepts a warning shorter than an idle time of half a minute or more', () => {
    const halves = (min, max) => fc.integer({ min, max }).map((n) => n / 2);
    fc.assert(
      fc.property(
        halves(1, 480).chain((timeout_min) =>
          fc.tuple(fc.constant(timeout_min), halves(0, timeout_min * 2 - 1)),
        ),
        ([timeout_min, warning_min]) => {
          expect(
            timeoutErrors({ enabled: true, timeout_min, warning_min }),
          ).toEqual({ timeout: null, warning: null });
          expect(
            timeoutErrors({
              enabled: true,
              timeout_min,
              warning_min: timeout_min + warning_min,
            }).warning,
          ).toBe(shorter(timeout_min));
        },
      ),
    );
  });
});

// the settings of a server that sends the minimega settings
const withMinimega = (source = 'default', read_connections = 16) => ({
  ...settings(),
  minimega_settings: { read_connections, source },
});

const readConnections = (page) => control(page, 'settings-read-connections');

describe('the minimega section of the Settings page', () => {
  it('is left out for a server that does not send the minimega settings', async () => {
    const page = await open();
    expect(page.find('#settings-minimega-help').exists()).toBe(false);
    expect(readConnections(page).exists()).toBe(false);
  });

  it('shows the read-only connections with their help', async () => {
    const page = await open(withMinimega());
    expect(page.findAll('h2').map(textOf)).toEqual([
      'Password Settings',
      'Timeout Settings',
      'File Logging Settings',
      'minimega',
    ]);
    expect(textOf(page.find('#settings-minimega-help'))).toBe(
      'Read-only connections caps how many read commands phēnix sends to minimega at the same time. phēnix keeps 2 of them for reads of the head node. A change applies at once, and phenix writes it to the config file.',
    );
    expect(textOf(page.find('#settings-read-connections'))).toBe(
      'Read-only connections',
    );
    const input = readConnections(page);
    expect(Number(input.element.value)).toBe(16);
    expect(input.element.disabled).toBe(false);
    expect(input.attributes('aria-describedby')).toBe(
      'settings-read-connections-help settings-minimega-help',
    );
    expect(textOf(page.find('#settings-read-connections-help'))).toBe(
      'Changes minimega.read-connections, from 3 to 64. The default is 16.',
    );
    expect(buttons(page)).toEqual(UNCHANGED);
  });

  it('sends an edited value with the other settings', async () => {
    axios.post.mockResolvedValue({});
    const page = await open(withMinimega());
    await readConnections(page).setValue('24');

    await page.find('form').trigger('submit');
    await flushPromises();
    expect(axios.post.mock.calls).toEqual([
      ['settings', withMinimega('default', 24), { timeout: 0 }],
    ]);
    expect(toasts().map((t) => t.text)).toEqual(['Settings updated']);
  });

  it.each(['2', '65', '7.5', ''])(
    'refuses %o before it sends anything',
    async (value) => {
      const page = await open(withMinimega());
      await readConnections(page).setValue(value);

      const input = readConnections(page);
      expect(input.attributes('aria-invalid')).toBe('true');
      expect(input.attributes('aria-describedby')).toBe(
        'settings-read-connections-error settings-read-connections-help',
      );
      expect(textOf(page.find('#settings-read-connections-error'))).toBe(
        'Enter a whole number from 3 to 64.',
      );
      expect(buttons(page)).toEqual(INVALID);

      await page.find('form').trigger('submit');
      expect(axios.post).not.toHaveBeenCalled();
    },
  );

  it.each([
    ['flag', 'A flag'],
    ['env', 'An environment variable'],
  ])(
    'is read-only, naming what sets it, when the source is %s',
    async (source, setBy) => {
      const page = await open(withMinimega(source, 20));
      const input = readConnections(page);
      expect(Number(input.element.value)).toBe(20);
      expect(input.element.disabled).toBe(true);
      expect(textOf(page.find('#settings-read-connections-source'))).toBe(
        `${setBy} of phenix ui sets this value, so you cannot change it here.`,
      );
      expect(page.find('#settings-read-connections-error').exists()).toBe(
        false,
      );
    },
  );

  it("shows the server's message when it refuses the value", async () => {
    const message =
      'error updating minimega settings: read_connections is out of range: it must be 3 to 64, not 70';
    axios.post.mockRejectedValueOnce(
      httpError(400, { message, code: 'invalid_request' }),
    );
    const page = await open(withMinimega());
    await readConnections(page).setValue('40');

    await page.find('form').trigger('submit');
    await flushPromises();
    expect(textOf(page.find('#settings-save-error'))).toBe(
      `The server did not save the settings: ${message}`,
    );
    expect(readConnections(page).element.value).toBe('40');
  });
});

describe('readConnectionsError', () => {
  const WHOLE = 'Enter a whole number from 3 to 64.';

  it.each([
    [3, null],
    [16, null],
    [64, null],
    ['20', null],
    [2, WHOLE],
    [65, WHOLE],
    [0, WHOLE],
    [7.5, WHOLE],
    ['', WHOLE],
    [null, WHOLE],
    ['many', WHOLE],
  ])('%o gives %o', (value, want) => {
    expect(readConnectionsError(value)).toBe(want);
  });

  it('accepts exactly the whole numbers from 3 to 64', () => {
    fc.assert(
      fc.property(fc.integer({ min: -100, max: 200 }), (n) => {
        expect(readConnectionsError(n) === null).toBe(n >= 3 && n <= 64);
      }),
    );
  });
});

describe('overriddenBy', () => {
  it.each([
    ['flag', 'A flag'],
    ['env', 'An environment variable'],
    ['config file', null],
    ['default', null],
    [undefined, null],
  ])('%o gives %o', (source, want) => {
    expect(overriddenBy(source)).toBe(want);
  });
});
