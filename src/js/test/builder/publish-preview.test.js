// What publishing changes, as the Publish dialog reads it
// (dialogs/publishPreview.js): marked busy from a change of the form on,
// read again after the pause, an answer asked for before a later change
// dropped, and the server's warnings listed with the changes. It uses fake
// timers, so it is a unit suite.

import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import {
  PREVIEW_DELAY_MS,
  usePublishPreview,
} from '@/components/builder/dialogs/publishPreview.js';

// publish.js reaches the API client, whose axios instance needs a browser.
vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ username: 'alice', role: null }),
}));

const INTENT = {
  mode: 'topology',
  topology: { name: 'lab', action: 'create' },
};

// What publishing to topology name changes: it creates it, and nothing else.
function changes(name) {
  return {
    topology: { name, action: 'create' },
    experiment: null,
    includes: [],
    scenarios: [],
    images: [],
    vlanAliases: [],
  };
}

// The store's answer to a dry run (see readPublishPreview in api.js).
function answer(name, { warningIssues = [], errorIssues = [] } = {}) {
  return {
    changes: name ? changes(name) : null,
    warningIssues,
    errorIssues,
  };
}

// Lets the reading a timer started take its answer.
async function settle() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

// An answer the test gives when it likes.
function later() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });

  return { promise, resolve };
}

function setup({
  idle = () => false,
  buildIntent = () => ({ intent: INTENT, error: '' }),
  publishable,
} = {}) {
  const previewPublish = vi.fn();
  const reader = usePublishPreview({
    buildIntent,
    previewPublish,
    idle,
    publishable,
  });

  return { ...reader, previewPublish };
}

const LEGACY_WARNING = {
  code: 'publish.legacy.replaced',
  severity: 'warning',
  message:
    'The legacy Builder diagram of topology lab was replaced by this diagram.',
};

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('what publishing changes', () => {
  test('is marked busy as soon as the form changes, and read again after the pause', async () => {
    const { preview, refresh, schedule, previewPublish } = setup();

    previewPublish.mockResolvedValueOnce(answer('lab'));
    await refresh();

    expect(preview.loading).toBe(false);
    expect(preview.changes.topology.name).toBe('lab');

    previewPublish.mockResolvedValueOnce(answer('lab-2'));
    schedule();

    // The lines read before stay shown, but no longer as current.
    expect(preview.loading).toBe(true);
    expect(preview.changes.topology.name).toBe('lab');

    await vi.advanceTimersByTimeAsync(PREVIEW_DELAY_MS - 1);

    expect(previewPublish).toHaveBeenCalledTimes(1);
    expect(preview.loading).toBe(true);

    await vi.advanceTimersByTimeAsync(1);
    await settle();

    expect(previewPublish).toHaveBeenCalledTimes(2);
    expect(preview.loading).toBe(false);
    expect(preview.changes.topology.name).toBe('lab-2');
  });

  test('drops an answer asked for before the form changed', async () => {
    const { preview, refresh, schedule, previewPublish } = setup();
    const first = later();

    previewPublish.mockReturnValueOnce(first.promise);

    const reading = refresh();

    // The form changes while the answer is on its way.
    schedule();
    first.resolve(answer('stale'));
    await reading;

    expect(preview.changes).toBeNull();
    expect(preview.loading).toBe(true);

    previewPublish.mockResolvedValueOnce(answer('current'));
    await vi.advanceTimersByTimeAsync(PREVIEW_DELAY_MS);
    await settle();

    expect(preview.changes.topology.name).toBe('current');
    expect(preview.loading).toBe(false);
  });

  test('lists the answer’s warnings with its changes, and says how many', async () => {
    const { preview, status, refresh, previewPublish } = setup();

    previewPublish.mockResolvedValueOnce(
      answer('lab', { warningIssues: [LEGACY_WARNING] }),
    );
    await refresh();

    expect(preview.changes.topology.name).toBe('lab');
    expect(preview.issues).toEqual([LEGACY_WARNING]);
    expect(status.text).toBe(
      'What publishing changes: Creates Topology config lab. ' +
        'Nothing outside the Topology changes. 1 warning below.',
    );
  });

  test('lists a refusal before the warnings, with no changes', async () => {
    const refusal = {
      code: 'publish.topology.exists',
      severity: 'error',
      message: 'config lab already exists',
    };
    const { preview, status, refresh, previewPublish } = setup();

    previewPublish.mockResolvedValueOnce(
      answer('', { errorIssues: [refusal], warningIssues: [LEGACY_WARNING] }),
    );
    await refresh();

    expect(preview.changes).toBeNull();
    expect(preview.issues).toEqual([refusal, LEGACY_WARNING]);
    expect(status.text).toBe(
      'What publishing changes: the server would refuse to publish.',
    );
  });

  test('an answer that cannot be read says Publish still works only while it does', async () => {
    const failure = {
      failed: true,
      message:
        'Could not work out what publishing changes: the server is down.',
    };
    let publishable = true;
    const { preview, status, refresh, previewPublish } = setup({
      publishable: () => publishable,
    });

    previewPublish.mockResolvedValueOnce(failure);
    await refresh();

    expect(preview.error).toBe(`${failure.message} You can still publish.`);
    expect(status.text).toBe(preview.error);

    // The checks' errors keep Publish unavailable.
    publishable = false;
    previewPublish.mockResolvedValueOnce(failure);
    await refresh();

    expect(preview.error).toBe(failure.message);
    expect(status.text).toBe(failure.message);
  });

  test('asks nothing for a form that makes no intent, and says why', async () => {
    const { preview, refresh, previewPublish } = setup({
      buildIntent: () => ({ intent: null, error: 'Enter a topology name.' }),
    });

    await refresh();

    expect(previewPublish).not.toHaveBeenCalled();
    expect(preview.loading).toBe(false);
    expect(preview.blocked).toBe('Enter a topology name.');
  });

  test('asks nothing while idle or once closed, and drops an answer that arrives after closing', async () => {
    let idle = true;
    const { preview, refresh, schedule, close, previewPublish } = setup({
      idle: () => idle,
    });

    schedule();
    await vi.advanceTimersByTimeAsync(PREVIEW_DELAY_MS);
    await refresh();

    expect(previewPublish).not.toHaveBeenCalled();

    idle = false;

    const late = later();

    previewPublish.mockReturnValueOnce(late.promise);

    const reading = refresh();

    close();
    late.resolve(answer('late'));
    await reading;

    schedule();
    await vi.advanceTimersByTimeAsync(PREVIEW_DELAY_MS);

    expect(previewPublish).toHaveBeenCalledTimes(1);
    expect(preview.changes).toBeNull();
  });
});
