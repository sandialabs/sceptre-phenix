// The Settings page: it loads the server's settings into a form, offers to
// reset or save only once something changed, and keeps unsaved edits when
// the settings are loaded again.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const axios = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));
const notify = vi.hoisted(() => vi.fn());
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: notify }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));
const loader = await vi.hoisted(() => import('./helpers/pageLoader.js'));
vi.mock('@/utils/pageLoader.js', loader.switchable);

import { clearPageCache } from '@/utils/pageCache.js';
import { pageStatus } from '@/utils/pageLoader.js';
import Settings from '@/views/Settings.vue';
import { flush } from './helpers/async.js';
import { makeContext } from './helpers/context.js';
import { buttons, renderSSR, textOf } from './helpers/render.js';

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

// the page once the server has sent it these settings
async function open(data = settings()) {
  axios.get.mockResolvedValueOnce({ data });
  const ctx = makeContext(Settings, { $buefy: { toast: { open: vi.fn() } } });
  Settings.created.call(ctx);
  await flush();
  return ctx;
}

// What the page shows as its state has it: its text, the switches that are
// on, and its buttons, marked when disabled or spinning.
async function shown(ctx) {
  const html = await loader.withoutLoading(() =>
    renderSSR(
      Settings,
      {},
      {
        route: '/settings',
        data: {
          loaded: ctx.loaded,
          saving: ctx.saving,
          saved: ctx.saved,
          settings_obj: ctx.settings_obj,
        },
      },
    ),
  );
  const form = html.match(/<form[\s\S]*<\/form>/)?.[0];
  return {
    text: form ? null : textOf(html),
    on: [...(form ?? '').matchAll(/<label class="switch[\s\S]*?<\/label>/g)]
      .filter(([sw]) => /<input[^>]*checked/.test(sw))
      .map(([sw]) => textOf(sw)),
    buttons: buttons(form ?? ''),
  };
}

beforeEach(() => {
  clearPageCache();
  axios.get.mockReset();
  axios.post.mockReset();
  notify.mockReset();
});

describe('the Settings page', () => {
  it("says it is loading, then shows the server's settings", async () => {
    axios.get.mockReturnValueOnce(new Promise(() => {}));
    const waiting = makeContext(Settings);
    Settings.created.call(waiting);
    expect(axios.get.mock.calls[0][0]).toBe('settings');
    expect((await shown(waiting)).text).toBe('Loading settings…');

    // another visit, which would otherwise join the load still waiting
    clearPageCache();
    const ctx = await open();
    expect(await shown(ctx)).toEqual({
      text: null,
      on: ['Require a lowercase letter'],
      buttons: ['Reset Form (disabled)', 'Save Changes (submits) (disabled)'],
    });
  });

  it('says so when the settings could not be loaded', async () => {
    const err = new Error('forbidden');
    axios.get.mockRejectedValueOnce(err);
    const ctx = makeContext(Settings);
    Settings.created.call(ctx);
    await flush();

    expect(notify).toHaveBeenCalledWith(err);
    expect(pageStatus.failed).toBe(true);
    expect((await shown(ctx)).text).toBe('Could not load settings');
  });

  it('puts back the settings last loaded on Reset Form', async () => {
    const ctx = await open();
    ctx.settings_obj.timeout_settings.enabled = true;
    expect(await shown(ctx)).toMatchObject({
      on: [
        'Require a lowercase letter',
        'Log out users after period of inactivity',
      ],
      buttons: ['Reset Form', 'Save Changes (submits)'],
    });

    ctx.resetForm();
    expect(ctx.settings_obj).toEqual(settings());
    expect((await shown(ctx)).buttons).toEqual([
      'Reset Form (disabled)',
      'Save Changes (submits) (disabled)',
    ]);
  });

  it('saves the changes once, however often the form is submitted', async () => {
    const answer = Promise.withResolvers();
    axios.post.mockReturnValue(answer.promise);
    const ctx = await open();

    // a submit with nothing changed sends nothing
    ctx.sendSettingsToServer();
    expect(axios.post).not.toHaveBeenCalled();

    ctx.settings_obj.password_settings.min_length = 12;
    ctx.sendSettingsToServer();
    ctx.sendSettingsToServer();
    const edited = settings();
    edited.password_settings.min_length = 12;
    expect(axios.post.mock.calls).toEqual([
      ['settings', edited, { timeout: 0 }],
    ]);
    expect((await shown(ctx)).buttons).toEqual([
      'Reset Form',
      'Save Changes (submits) (spinning)',
    ]);

    answer.resolve({});
    await flush();
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Settings updated' }),
    );
    expect((await shown(ctx)).buttons).toEqual([
      'Reset Form (disabled)',
      'Save Changes (submits) (disabled)',
    ]);

    // the page opens with the saved settings next time, before they load
    axios.get.mockReturnValueOnce(new Promise(() => {}));
    const next = makeContext(Settings);
    Settings.created.call(next);
    expect(next.settings_obj).toEqual(edited);
  });

  it('keeps the edits and reports a save that fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const ctx = await open();
    ctx.settings_obj.logging_settings.max_file_age = 30;

    ctx.sendSettingsToServer();
    await flush();
    expect(notify).toHaveBeenCalledWith(err);
    expect(ctx.settings_obj.logging_settings.max_file_age).toBe(30);
    expect((await shown(ctx)).buttons).toEqual([
      'Reset Form',
      'Save Changes (submits)',
    ]);
  });

  it('keeps unsaved edits when the settings load again', async () => {
    const ctx = await open();
    ctx.settings_obj.password_settings.symbol_req = true;

    const changed = settings();
    changed.timeout_settings.timeout_min = 60;
    axios.get.mockResolvedValueOnce({ data: changed });
    await pageStatus.refresh();

    expect(ctx.settings_obj.password_settings.symbol_req).toBe(true);
    expect(ctx.settings_obj.timeout_settings.timeout_min).toBe(30);
    ctx.resetForm();
    expect(ctx.settings_obj).toEqual(changed);
  });
});
