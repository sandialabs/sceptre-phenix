// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import Buefy from 'buefy';

import Settings, { timeoutErrors } from '@/views/Settings.vue';
import axiosInstance from '@/utils/axios.js';

vi.mock('@/utils/axios.js', () => ({
  default: { get: vi.fn(), post: vi.fn() },
}));
vi.mock('@/utils/errorNotif', () => ({ useErrorNotification: vi.fn() }));

enableAutoUnmount(afterEach);

const settings = (timeout_settings) => ({
  password_settings: {
    number_req: false,
    symbol_req: false,
    lowercase_req: false,
    uppercase_req: false,
    min_length: 8,
  },
  timeout_settings,
  logging_settings: {
    max_file_age: 365,
    max_file_rotations: 3,
    max_file_size: 100,
  },
});

describe('timeoutErrors', () => {
  test.each([
    [{ enabled: true, timeout_min: 30, warning_min: 3 }, null, null],
    [{ enabled: true, timeout_min: 30, warning_min: 0 }, null, null],
    [{ enabled: true, timeout_min: 0.5, warning_min: 0 }, null, null],
    [{ enabled: true, timeout_min: 1, warning_min: 0.5 }, null, null],
    [{ enabled: false, timeout_min: 0, warning_min: 50 }, null, null],
    [{ enabled: false, timeout_min: null, warning_min: -1 }, null, null],
    [
      { enabled: true, timeout_min: 10, warning_min: 10 },
      null,
      'Enter a warning time of less than the idle time (10 minutes).',
    ],
    [
      { enabled: true, timeout_min: 10, warning_min: 15 },
      null,
      'Enter a warning time of less than the idle time (10 minutes).',
    ],
    [
      { enabled: true, timeout_min: 0, warning_min: 0 },
      'Enter an idle time of 0.5 minutes or more.',
      null,
    ],
    [
      { enabled: true, timeout_min: 0.4, warning_min: 0 },
      'Enter an idle time of 0.5 minutes or more.',
      null,
    ],
    [
      { enabled: true, timeout_min: null, warning_min: 0 },
      'Enter an idle time of 0.5 minutes or more.',
      null,
    ],
    [
      { enabled: true, timeout_min: 0.5, warning_min: 0.5 },
      null,
      'Enter a warning time of less than the idle time (0.5 minutes).',
    ],
    [
      { enabled: true, timeout_min: 30, warning_min: -1 },
      null,
      'Enter a warning time of 0 minutes or more.',
    ],
  ])('%o', (timeout, timeoutError, warningError) => {
    expect(timeoutErrors(timeout)).toEqual({
      timeout: timeoutError,
      warning: warningError,
    });
  });
});

describe('Settings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  async function mountSettings(timeout) {
    axiosInstance.get.mockResolvedValue({ data: settings(timeout) });
    const wrapper = mount(Settings, {
      attachTo: document.body,
      global: { plugins: [Buefy] },
    });
    await flushPromises();
    return wrapper;
  }

  test('has one h1 and section headings at the next level', async () => {
    const wrapper = await mountSettings({
      enabled: false,
      timeout_min: 30,
      warning_min: 3,
    });

    expect(wrapper.findAll('h1').map((h) => h.text())).toEqual(['Settings']);
    expect(wrapper.findAll('h2').map((h) => h.text())).toEqual([
      'Password Settings',
      'Timeout Settings',
      'File Logging Settings',
    ]);
    expect(wrapper.find('h3').exists()).toBe(false);
  });

  test('a warning as long as the timeout shows an inline error and blocks Save', async () => {
    const wrapper = await mountSettings({
      enabled: true,
      timeout_min: 10,
      warning_min: 10,
    });

    const warning = wrapper.get(
      'input[aria-labelledby="settings-idle-warning"]',
    );
    expect(warning.attributes('aria-invalid')).toBe('true');
    expect(warning.attributes('aria-describedby')).toBe(
      'settings-idle-warning-error',
    );
    expect(wrapper.get('#settings-idle-warning-error').text()).toBe(
      'Enter a warning time of less than the idle time (10 minutes).',
    );

    const save = wrapper
      .findAll('button')
      .find((b) => b.text() === 'Save Changes');
    expect(save.attributes()).toHaveProperty('disabled');

    await wrapper.get('form').trigger('submit');
    expect(axiosInstance.post).not.toHaveBeenCalled();
  });

  test('valid settings save', async () => {
    axiosInstance.post.mockResolvedValue({});
    const wrapper = await mountSettings({
      enabled: true,
      timeout_min: 10,
      warning_min: 2,
    });

    expect(wrapper.find('#settings-idle-warning-error').exists()).toBe(false);
    await wrapper.get('form').trigger('submit');
    expect(axiosInstance.post).toHaveBeenCalledOnce();
  });

  test('a disabled idle timeout does not block Save', async () => {
    axiosInstance.post.mockResolvedValue({});
    const wrapper = await mountSettings({
      enabled: false,
      timeout_min: 0,
      warning_min: 5,
    });

    expect(wrapper.find('#settings-idle-timeout-error').exists()).toBe(false);
    expect(wrapper.find('#settings-idle-warning-error').exists()).toBe(false);
    await wrapper.get('form').trigger('submit');
    expect(axiosInstance.post).toHaveBeenCalledOnce();
  });
});
