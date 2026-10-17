// @vitest-environment happy-dom
// The Publish dialog, mounted and driven as a user would: what publishing
// changes as the form changes, and a partial publication's result.

import { afterEach, describe, expect, test, vi } from 'vitest';
import { createPinia } from 'pinia';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';

import PublishDialog from '@/components/builder/dialogs/PublishDialog.vue';
import { PREVIEW_DELAY_MS } from '@/components/builder/dialogs/publishPreview.js';
import { connect } from '@/builder/model.js';
import { useBuilderStore } from '@/builder/store.js';
import axiosInstance from '@/utils/axios.js';

import { sampleDocument } from './fixtures.js';

// Only Publish itself reaches the client: the store's other reads are spied.
vi.mock('@/utils/axios.js', () => ({ default: { post: vi.fn() } }));

enableAutoUnmount(afterEach);

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

// The sample diagram with every interface connected, which publishes; or as
// it is, with an interface that has no VLAN, which Publish refuses.
function diagram({ publishable }) {
  const { doc, sw, bravo } = sampleDocument();

  return publishable
    ? connect(doc, {
        sourceNodeId: bravo.id,
        sourceHandleId: bravo.device.interfaces[0].id,
        targetNodeId: sw.id,
      }).doc
    : doc;
}

// Opens the dialog in the page. `setup` changes the store first, and
// `previewPublish` answers each dry run.
function open({
  publishable = true,
  setup = () => {},
  previewPublish = async () => null,
} = {}) {
  const pinia = createPinia();
  const store = useBuilderStore(pinia);

  store.doc = diagram({ publishable });
  setup(store);

  vi.spyOn(store, 'fetchSources').mockResolvedValue(store.sources);
  vi.spyOn(store, 'fetchDocuments').mockResolvedValue([]);
  vi.spyOn(store, 'previewPublish').mockImplementation(previewPublish);

  const wrapper = mount(PublishDialog, {
    attachTo: document.body,
    global: { plugins: [pinia] },
  });

  return { wrapper, store, pinia };
}

const byId = (wrapper, testId) => wrapper.find(`[data-testid="${testId}"]`);

// What publishing changes, as its section shows it.
const section = (wrapper) => byId(wrapper, 'publish-preview');

// Resolves to a promise the test settles, and the function that settles it.
function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });

  return { promise, resolve };
}

// A dry run's answer: topology `name` is updated, and nothing else changes.
const changesTo = (name) => ({
  topology: { name, action: 'update' },
  experiment: null,
  includes: [],
  scenarios: [],
  images: [],
  vlanAliases: [],
});

describe('what publishing changes', () => {
  const changes = changesTo('lab');
  const legacyWarning = {
    code: 'publish.legacy.replaced',
    severity: 'warning',
    message:
      'The legacy Builder diagram of topology lab was replaced by this diagram.',
  };

  test('lists the dry run’s warnings after its changes, by severity and with their codes', async () => {
    const { wrapper } = open({
      previewPublish: async () => ({
        changes,
        errorIssues: [],
        warningIssues: [legacyWarning],
      }),
    });

    await flushPromises();

    const shown = section(wrapper);
    const issues = byId(shown, 'publish-preview-issues');

    expect(shown.attributes('aria-busy')).toBe('false');
    expect(shown.text()).toContain('Updates Topology config lab');
    expect(issues.exists()).toBe(true);
    expect(byId(shown, 'publish-preview-issues-warning').exists()).toBe(true);
    expect(byId(shown, 'publish-preview-issues-error').exists()).toBe(false);
    expect(shown.text()).toContain('1 warning');
    expect(issues.get('strong').text()).toBe('Warning:');
    expect(byId(issues, 'issue-code').text()).toBe('publish.legacy.replaced');
    expect(
      byId(shown, 'publish-preview-configs').element.compareDocumentPosition(
        issues.element,
      ) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  test('while it is read again, keeps the last answer shown, marked busy', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    const answers = [deferred(), deferred()];
    const { wrapper, store } = open({
      previewPublish: () =>
        answers[store.previewPublish.mock.calls.length - 1].promise,
    });

    await flushPromises();
    answers[0].resolve({ changes, errorIssues: [], warningIssues: [] });
    await flushPromises();
    expect(section(wrapper).attributes('aria-busy')).toBe('false');

    await byId(wrapper, 'publish-name').setValue('lab-2');

    // Busy at once, while the pause in typing runs, and while it is read.
    for (const wait of [0, PREVIEW_DELAY_MS]) {
      vi.advanceTimersByTime(wait);
      await flushPromises();

      expect(section(wrapper).attributes('aria-busy')).toBe('true');
      expect(section(wrapper).text()).toContain('Updates Topology config lab');
      expect(section(wrapper).text()).not.toContain(
        'Calculating what publishing changes',
      );
    }

    expect(store.previewPublish).toHaveBeenCalledTimes(2);
    answers[1].resolve({
      changes: changesTo('lab-2'),
      errorIssues: [],
      warningIssues: [],
    });
    await flushPromises();

    expect(section(wrapper).attributes('aria-busy')).toBe('false');
    expect(section(wrapper).text()).toContain('Updates Topology config lab-2');
  });

  test('the topology, experiment and scenario are read once typing pauses, and only the latest answer shows, whichever comes first', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    for (const latestFirst of [true, false]) {
      const answers = [deferred(), deferred(), deferred()];
      const { wrapper, store } = open({
        setup: (builder) => {
          builder.doc = {
            ...builder.doc,
            scenarios: ['plant-ntp', 'plant-dns'],
          };
        },
        previewPublish: () =>
          answers[store.previewPublish.mock.calls.length - 1].promise,
      });

      await flushPromises();
      answers[0].resolve({ changes, errorIssues: [], warningIssues: [] });
      await flushPromises();

      await byId(wrapper, 'publish-mode-experiment').setValue();
      await byId(wrapper, 'publish-name').setValue('plant');
      await byId(wrapper, 'publish-experiment').setValue('run-1');
      await byId(wrapper, 'publish-scenario-select').setValue('plant-dns');

      // Each change restarts the pause: nothing is asked until it ends.
      vi.advanceTimersByTime(PREVIEW_DELAY_MS - 1);
      await flushPromises();
      expect(store.previewPublish).toHaveBeenCalledTimes(1);
      vi.advanceTimersByTime(1);
      await flushPromises();
      expect(store.previewPublish).toHaveBeenCalledTimes(2);
      expect(store.previewPublish).toHaveBeenLastCalledWith({
        mode: 'topology-experiment',
        topology: { name: 'plant', action: 'create' },
        scenario: { name: 'plant-dns' },
        experiment: { name: 'run-1', action: 'create' },
      });

      // A newer form is read before the older answer comes.
      await byId(wrapper, 'publish-experiment').setValue('run-2');
      vi.advanceTimersByTime(PREVIEW_DELAY_MS);
      await flushPromises();
      expect(store.previewPublish).toHaveBeenCalledTimes(3);

      const older = {
        changes: changesTo('older'),
        errorIssues: [],
        warningIssues: [],
      };
      const latest = {
        changes: changesTo('latest'),
        errorIssues: [],
        warningIssues: [],
      };
      const order = latestFirst
        ? [() => answers[2].resolve(latest), () => answers[1].resolve(older)]
        : [() => answers[1].resolve(older), () => answers[2].resolve(latest)];

      order[0]();
      await flushPromises();
      expect(
        section(wrapper).attributes('aria-busy'),
        `latest first: ${latestFirst}`,
      ).toBe(latestFirst ? 'false' : 'true');
      expect(section(wrapper).text()).not.toContain('Topology config older');

      order[1]();
      await flushPromises();
      expect(section(wrapper).attributes('aria-busy')).toBe('false');
      expect(section(wrapper).text()).toContain(
        'Updates Topology config latest',
      );
      expect(section(wrapper).text()).not.toContain('Topology config older');

      wrapper.unmount();
    }
  });

  test('a refusal is listed as errors, with no changes', async () => {
    const refusal = {
      code: 'publish.topology.exists',
      severity: 'error',
      message: 'config lab already exists',
    };
    const { wrapper } = open({
      previewPublish: async () => ({
        changes: null,
        errorIssues: [refusal],
        warningIssues: [legacyWarning],
      }),
    });

    await flushPromises();

    const shown = section(wrapper);

    expect(byId(shown, 'publish-preview-issues-error').exists()).toBe(true);
    expect(byId(shown, 'publish-preview-issues-warning').exists()).toBe(true);
    expect(shown.text()).toContain('1 error blocks publishing');
    expect(byId(shown, 'publish-preview-configs').exists()).toBe(false);
    expect(byId(shown, 'publish-preview-checks').exists()).toBe(false);
  });

  test('a refusal leaves out what the checks list, and says their errors block publishing', async () => {
    const clash = {
      code: 'publish.include.clash',
      severity: 'error',
      message: 'hostname "bravo" is also a device of included topology plant',
    };
    let errorIssues = [];
    // The server refuses bravo's eth0, which has no VLAN, as the checks do.
    const setup = (store) => {
      const bravo = store.doc.nodes.find(
        (node) => node.device?.hostname === 'bravo',
      );

      errorIssues = [
        {
          code: 'interface.vlan.missing',
          severity: 'error',
          message:
            'interface "eth0" of device "bravo" has no VLAN: connect it to a network, or type a VLAN for it',
          nodeId: bravo.id,
        },
        clash,
      ];
    };
    const { wrapper } = open({
      publishable: false,
      setup,
      previewPublish: async () => ({
        changes: null,
        errorIssues,
        warningIssues: [],
      }),
    });

    await flushPromises();

    const text = section(wrapper).text();

    expect(text).toContain('The errors listed under Checks block publishing.');
    expect(text).toContain('1 error blocks publishing');
    expect(text).toContain(clash.message);
    expect(text).not.toContain('has no VLAN');
  });

  test('a problem only the server finds is listed once after a refused Publish, whichever answer came first', async () => {
    const missing = {
      code: 'publish.scenario.missing',
      severity: 'error',
      message: 'scenario plant-ntp does not exist',
    };

    for (const refusalFirst of [true, false]) {
      const { wrapper, store } = open({
        previewPublish: async () => ({
          changes: null,
          errorIssues: [missing],
          warningIssues: [],
        }),
      });

      // The dialog clears the refusal when it opens, and asks before the
      // answer comes.
      if (refusalFirst) {
        store.publishIssues = [missing];
      }

      await flushPromises();

      if (!refusalFirst) {
        store.publishIssues = [missing];
        await flushPromises();
      }

      const refusal = byId(wrapper, 'publish-refusal');

      expect(refusal.exists(), `refusal first: ${refusalFirst}`).toBe(true);
      expect(refusal.text()).toContain(missing.message);
      expect(section(wrapper).text()).not.toContain(missing.message);
      expect(byId(wrapper, 'publish-preview-issues').exists()).toBe(false);
      expect(wrapper.text().split(missing.message)).toHaveLength(2);

      wrapper.unmount();
    }
  });

  test('a dry run that cannot be read says Publish still works only while it does', async () => {
    const failure = {
      failed: true,
      message: 'Could not read what publishing changes: the server is down.',
    };

    for (const publishable of [true, false]) {
      const { wrapper } = open({
        publishable,
        previewPublish: async () => failure,
      });

      await flushPromises();

      const shown = byId(section(wrapper), 'publish-preview-error').text();
      const submit = byId(wrapper, 'publish-submit');

      if (publishable) {
        expect(shown).toBe(`${failure.message} You can still publish.`);
        expect(submit.attributes()).not.toHaveProperty('disabled');
      } else {
        // The checks' errors keep Publish unavailable, which nothing
        // contradicts.
        expect(shown).toBe(failure.message);
        expect(submit.attributes()).toHaveProperty('disabled');
        expect(wrapper.text()).not.toContain('You can still publish');
      }

      wrapper.unmount();
    }
  });
});

describe('a publication that fails part way', () => {
  // The server's partial-failure answer (writePublishPartial), as
  // builder-publish.spec.js mocks it.
  const partial = (topology) => ({
    status: 'partial',
    stages: [
      {
        name: 'document',
        status: 'created',
        message: 'immutable builder document stored',
      },
      { name: 'topology', status: 'created', config: `Topology/${topology}` },
      {
        name: 'experiment',
        status: 'failed',
        message: 'experiment publication failed',
      },
    ],
    warnings: [],
    errors: ['experiment publication failed'],
  });

  // A draft whose changes the server holds, which store.publish requires.
  function savedDraft(store) {
    const saved = { ...store.saveState, status: 'saved', pending: 0 };

    store.draftId = 'd1';
    store.saveState = saved;
    store.autosave = {
      flush: async () => saved,
      hold: () => () => {},
      idle: async () => {},
      sent: 0,
      setETag: async () => {},
      dispose: () => {},
    };
  }

  test('lists the failed stage, moves focus to the result, and Back restores the form', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });

    const topology = 'partial';
    const experiment = 'partial-exp';

    axiosInstance.post.mockRejectedValue({
      response: { status: 500, data: partial(topology) },
    });

    const { wrapper, store } = open({
      setup: savedDraft,
      previewPublish: async () => ({
        changes: changesTo(topology),
        errorIssues: [],
        warningIssues: [],
      }),
    });

    await flushPromises();
    await byId(wrapper, 'publish-mode-experiment').setValue();
    await byId(wrapper, 'publish-name').setValue(topology);
    await byId(wrapper, 'publish-experiment').setValue(experiment);

    const submit = byId(wrapper, 'publish-submit');

    expect(submit.text()).toBe('Create topology and experiment');
    await submit.trigger('click');
    await flushPromises();

    expect(axiosInstance.post).toHaveBeenCalledWith(
      'builder/drafts/d1/publish',
      expect.objectContaining({
        topology: { name: topology, action: 'create' },
        experiment: { name: experiment, action: 'create' },
      }),
      expect.anything(),
    );

    // The result replaces the form, and focus moves to its summary.
    const result = byId(wrapper, 'publish-result');
    const summary = byId(result, 'publish-summary');

    expect(wrapper.find('form').exists()).toBe(false);
    expect(summary.text()).toBe(
      'Published with failures. Some configs were written. The failed stages are listed below.',
    );
    expect(document.activeElement).toBe(summary.element);
    expect(
      Object.fromEntries(
        result
          .findAll('li[data-status]')
          .map((item) => [
            item.get('strong').text().replace(/:$/, ''),
            item.attributes('data-status'),
          ]),
      ),
    ).toEqual({
      document: 'created',
      topology: 'created',
      experiment: 'failed',
    });
    expect(
      result.get('li[data-status="failed"]').attributes('data-level'),
    ).toBe('error');
    expect(
      result.get('li[data-status="created"]').attributes('data-level'),
    ).toBe('info');
    expect(result.get('li[data-level="error"]:not([data-status])').text()).toBe(
      'Error: experiment publication failed',
    );

    // Under a heading that counts the errors; it names no element of the
    // diagram, so it has no Go to.
    const reported = byId(result, 'publish-result-issues');

    expect(reported.findAll('h3').map((heading) => heading.text())).toEqual([
      '1 error',
    ]);
    expect(byId(reported, 'issue-go-to').exists()).toBe(false);
    // The dialog reports the failure in place of its form's alert. The page
    // holds its own alert and announcement while the modal is open (e2e).
    expect(byId(wrapper, 'publish-error').exists()).toBe(false);
    expect(store.error).toBe(
      'Publish finished with failures. Some configs were written.',
    );
    expect(wrapper.emitted('published')).toBeUndefined();

    await byId(result, 'publish-retry').trigger('click');
    await flushPromises();

    expect(byId(wrapper, 'publish-result').exists()).toBe(false);
    expect(byId(wrapper, 'publish-name').element.value).toBe(topology);
    expect(byId(wrapper, 'publish-experiment').element.value).toBe(experiment);
    expect(byId(wrapper, 'publish-submit').attributes()).not.toHaveProperty(
      'disabled',
    );
    // Back replaces the result, and the button that had focus, with the form.
    expect(document.activeElement).toBe(
      byId(wrapper, 'publish-submit').element,
    );
  });
});
