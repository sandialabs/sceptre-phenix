// The netflow tab's Download captures button and dialog: the recorded
// captures it lists, and the downloads it starts.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);

import NetflowCaptures from '@/components/experiment/NetflowCaptures.vue';
import { componentDeclarations } from './helpers/css.js';
import { httpError } from './helpers/fixtures.js';
import { mountComponent, textOf } from './helpers/mount.js';

// the anchors clicked to download, as their href and download name
let clicked;

beforeEach(() => {
  resetMocks();
  clicked = [];
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
    function () {
      clicked.push([this.getAttribute('href'), this.download]);
    },
  );
});

afterEach(() => vi.restoreAllMocks());

const capture = (fields = {}) => ({
  id: '20261028T153012.345Z',
  started: '2026-10-28T15:30:12.345Z',
  stopped: '2026-10-28T16:00:00Z',
  state: 'stopped',
  size: 2048,
  flows: 1234,
  truncated: false,
  path: 'netflow/20261028T153012.345Z.txt',
  ...fields,
});

// the button, mounted for experiment demo, once its dialog opened and the
// list arrived
async function openDialog(captures, experiment = 'demo') {
  if (captures instanceof Error) axios.get.mockRejectedValue(captures);
  else axios.get.mockResolvedValue({ data: { captures } });
  const wrapper = await mountComponent(NetflowCaptures, {
    props: { experiment },
  });
  await wrapper.find('button').trigger('click');
  await flushPromises();
  return wrapper;
}

const rows = (wrapper) => wrapper.findAll('tbody tr');
const cells = (row) => row.findAll('td').map((td) => textOf(td));
const buttonNamed = (wrapper, text) =>
  wrapper.findAll('button').find((b) => textOf(b) === text);

describe('the dialog', () => {
  it('opens from the button and lists the captures in the server order', async () => {
    const wrapper = await openDialog([
      capture({ id: 'b', state: 'recording', stopped: null, flows: 5 }),
      capture({ id: 'a', state: 'interrupted', stopped: null, flows: null }),
      capture({ truncated: true }),
    ]);

    expect(axios.get).toHaveBeenCalledWith('experiments/demo/netflow/captures');
    expect(textOf(wrapper.find('.modal-card-title'))).toBe(
      'Recorded netflow captures',
    );
    expect(textOf(wrapper.find('caption'))).toBe(
      'Recorded netflow captures, newest first',
    );
    expect(wrapper.findAll('th').map((th) => th.attributes('scope'))).toEqual([
      'col',
      'col',
      'col',
      'col',
      'col',
    ]);

    const [recording, interrupted, truncated] = rows(wrapper).map(cells);
    expect(recording.slice(1, 4)).toEqual(['Recording', '5', '2.0 KiB']);
    expect(interrupted.slice(1, 3)).toEqual(['Interrupted', 'Unknown']);
    expect(truncated[2]).toBe('1,234 Truncated');
    expect(textOf(wrapper.find('.netflow-captures-note'))).toContain(
      'ui.netflow.max-size',
    );
    expect(textOf(wrapper.find('.netflow-captures-note'))).toContain(
      'write error',
    );
  });

  it('shows when each started and stopped as the server sent it, then how long ago', async () => {
    vi.useFakeTimers({
      toFake: ['Date'],
      now: Date.parse('2026-10-28T18:00:00Z'),
    });
    try {
      const wrapper = await openDialog([capture()]);
      const [started, stopped] = rows(wrapper)[0].findAll('td');
      expect(textOf(started)).toBe('2026-10-28T15:30:12Z (2 hours ago)');
      expect(textOf(stopped)).toBe('2026-10-28T16:00:00Z (2 hours ago)');
      expect(started.find('time').attributes('datetime')).toBe(
        '2026-10-28T15:30:12.345Z',
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('lists a running capture that stopped recording as Live only', async () => {
    const wrapper = await openDialog([
      capture({ state: 'recording', stopped: null, truncated: true }),
    ]);
    expect(cells(rows(wrapper)[0])[1]).toBe('Live only');
  });

  it('announces its state in a status region', async () => {
    const wrapper = await openDialog([]);
    expect(textOf(wrapper.find('[role="status"]'))).toBe(
      'No captures recorded yet. Start a capture with the netflow button to record one.',
    );
  });

  it('notes truncation only when a capture is truncated', async () => {
    const wrapper = await openDialog([capture()]);
    expect(wrapper.find('.netflow-captures-note').exists()).toBe(false);
  });

  it('says when there are no captures', async () => {
    const wrapper = await openDialog([]);
    expect(textOf(wrapper.find('.modal-card-body'))).toBe(
      'No captures recorded yet. Start a capture with the netflow button to record one.',
    );
  });

  it('reports a failed list, and tries again on Retry and on Refresh', async () => {
    const failure = httpError(500, { message: 'boom' });
    const wrapper = await openDialog(failure);
    expect(notify.error).toHaveBeenCalledWith(failure);
    expect(textOf(wrapper.find('.modal-card-body'))).toContain(
      'Could not load captures.',
    );

    axios.get.mockResolvedValue({ data: { captures: [capture()] } });
    await buttonNamed(wrapper, 'Retry').trigger('click');
    await flushPromises();
    expect(rows(wrapper)).toHaveLength(1);

    await buttonNamed(wrapper, 'Refresh').trigger('click');
    await flushPromises();
    expect(axios.get).toHaveBeenCalledTimes(3);
  });

  it('names each download button by its capture and format', async () => {
    const wrapper = await openDialog([capture()]);
    const started = '2026-10-28T15:30:12Z';
    const labels = rows(wrapper)[0]
      .findAll('button')
      .map((b) => [textOf(b), b.attributes('aria-label')]);
    expect(labels).toEqual([
      ['Text', `Download capture started ${started} as text`],
      ['CSV', `Download capture started ${started} as CSV`],
      ['JSONL', `Download capture started ${started} as JSONL`],
    ]);
    for (const [text, label] of labels) {
      expect(label.toLowerCase()).toContain(text.toLowerCase());
    }
  });
});

describe('a download', () => {
  it('checks the capture is there, then downloads it through a link', async () => {
    const wrapper = await openDialog([capture()]);
    axios.head.mockResolvedValue({ status: 200 });

    await buttonNamed(wrapper, 'CSV').trigger('click');
    await flushPromises();

    expect(axios.head).toHaveBeenCalledWith(
      'experiments/demo/netflow/captures/20261028T153012.345Z',
      { params: { format: 'csv' } },
    );
    expect(clicked).toEqual([
      [
        '/api/v1/experiments/demo/netflow/captures/20261028T153012.345Z?format=csv&token=t',
        'demo-netflow-20261028T153012.345Z.csv',
      ],
    ]);
  });

  it('reports a capture gone since the list, and lists again', async () => {
    const wrapper = await openDialog([capture()]);
    axios.head.mockRejectedValue(httpError(404));

    await buttonNamed(wrapper, 'JSONL').trigger('click');
    await flushPromises();

    expect(notify.error).toHaveBeenCalledWith(
      'Netflow capture 20261028T153012.345Z is no longer available.',
    );
    expect(clicked).toEqual([]);
    expect(axios.get).toHaveBeenCalledTimes(2);
  });

  it('builds a link for every format of every capture', async () => {
    await fc.assert(
      fc.asyncProperty(
        fc.uniqueArray(fc.stringMatching(/^[0-9A-Za-z.:-]{1,24}$/), {
          minLength: 1,
          maxLength: 4,
        }),
        fc.string({ minLength: 1, maxLength: 10 }),
        async (ids, experiment) => {
          resetMocks();
          clicked = [];
          axios.head.mockResolvedValue({ status: 200 });
          const wrapper = await openDialog(
            ids.map((id) => capture({ id })),
            experiment,
          );
          for (const [i, row] of rows(wrapper).entries()) {
            const formats = row.findAll('button');
            expect(formats).toHaveLength(3);
            for (const button of formats) await button.trigger('click');
            await flushPromises();
            expect(clicked.slice(-3).map(([href]) => href)).toEqual(
              ['text', 'csv', 'jsonl'].map(
                (format) =>
                  `/api/v1/experiments/${encodeURIComponent(experiment)}` +
                  `/netflow/captures/${encodeURIComponent(ids[i])}` +
                  `?format=${format}&token=t`,
              ),
            );
          }
          wrapper.unmount();
        },
      ),
      { numRuns: 20 },
    );
  });
});

describe('focus', () => {
  // the button and its dialog attached to the document, after the list
  // failed to load
  async function attachFailed() {
    axios.get.mockRejectedValue(httpError(500));
    const wrapper = await mountComponent(NetflowCaptures, {
      props: { experiment: 'demo' },
      attachTo: document.body,
    });
    await wrapper.find('button').trigger('click');
    await flushPromises();
    return wrapper;
  }

  it('keeps Retry while it lists again, then stays in the dialog', async () => {
    const wrapper = await attachFailed();
    let resolve;
    axios.get.mockReturnValue(new Promise((r) => (resolve = r)));
    const retry = buttonNamed(wrapper, 'Retry');
    retry.element.focus();
    await retry.trigger('click');
    await flushPromises();

    expect(buttonNamed(wrapper, 'Retry').element).toBe(retry.element);
    expect(textOf(wrapper.find('[role="status"]'))).toBe('Loading captures…');

    resolve({ data: { captures: [capture()] } });
    await flushPromises();
    expect(rows(wrapper)).toHaveLength(1);
    expect(document.activeElement).toBe(
      wrapper.find('.modal-card-body').element,
    );
    wrapper.unmount();
  });

  it('keeps the list while it refreshes', async () => {
    axios.get.mockResolvedValue({ data: { captures: [capture()] } });
    const wrapper = await mountComponent(NetflowCaptures, {
      props: { experiment: 'demo' },
      attachTo: document.body,
    });
    await wrapper.find('button').trigger('click');
    await flushPromises();

    axios.get.mockReturnValue(new Promise(() => {}));
    const refresh = buttonNamed(wrapper, 'Refresh');
    refresh.element.focus();
    await refresh.trigger('click');
    await flushPromises();

    expect(rows(wrapper)).toHaveLength(1);
    expect(refresh.attributes('disabled')).toBeUndefined();
    expect(document.activeElement).toBe(refresh.element);
    wrapper.unmount();
  });
});

describe('closing the dialog', () => {
  it('returns focus to the button that opened it', async () => {
    axios.get.mockResolvedValue({ data: { captures: [] } });
    const wrapper = await mountComponent(NetflowCaptures, {
      props: { experiment: 'demo' },
      attachTo: document.body,
    });
    // a keyboard user's focus is on the button that opens it
    const trigger = wrapper.find('button');
    trigger.element.focus();
    await trigger.trigger('click');
    await flushPromises();

    await wrapper.find('button[aria-label="Close"]').trigger('click');
    await flushPromises();
    expect(document.activeElement).toBe(trigger.element);
  });
});

// The dialog opens inside the running experiment page's .content, whose h2
// and table cell rules would restyle its title and cells.
describe('the dialog styles', () => {
  const style = (selector) =>
    componentDeclarations(
      'src/components/experiment/NetflowCaptures.vue',
      selector,
    );

  it('gives its h2 title the size and spacing of other dialog titles', () => {
    expect(style('.netflow-captures-card .modal-card-title')).toEqual({
      'margin-bottom': '0',
      'font-size': '1.5rem',
      'font-weight': '400',
      'line-height': '1.25',
    });
  });

  it('gives the Recording tag a blue white text reads on', () => {
    expect(style('.netflow-captures-table .tag.is-info')).toEqual({
      'background-color': '#296fa8',
    });
  });

  it('centers each row vertically on its download buttons', () => {
    expect(style('.netflow-captures-table td')).toEqual({
      'vertical-align': 'middle',
    });
  });
});
