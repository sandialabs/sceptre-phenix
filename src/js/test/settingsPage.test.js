// The Settings page: loads the settings into a form, offers reset and save
// only after a change, and keeps unsaved edits when the settings reload.
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
    expect(page.vm.settings_obj).toEqual(settings());
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
