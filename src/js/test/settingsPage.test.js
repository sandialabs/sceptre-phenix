// The Settings page: loads the settings into a form, offers reset and save
// only after a change, and keeps unsaved edits when the settings reload.
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
import Settings from '@/views/Settings.vue';
import { never } from './helpers/async.js';
import { buttons, mountPage, textOf, toasts } from './helpers/mount.js';

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
    expect(textOf(waiting)).toBe('Loading settings…');
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
    expect(textOf(page)).toBe('Could not load settings');
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
        expect(buttons(page)).toEqual(changed ? CHANGED : UNCHANGED);
        await page.find('form').trigger('submit');
        await flushPromises();
        expect(axios.post.mock.calls.map(([, sent]) => sent)).toEqual(
          changed ? [want] : [],
        );
        page.unmount();
      }),
      { numRuns: 15 },
    );
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
});
