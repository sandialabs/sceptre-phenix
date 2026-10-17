// The experiment Files tab (ExperimentFilesTab.vue): listing, opening,
// selecting, downloading and deleting files, and the pages that hold it.
import fc from 'fast-check';
import { flushPromises, RouterLinkStub } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notices, noticeText, notify, rbac, resetMocks, store } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
// the experiment pages around the tab
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);
vi.mock('@/utils/notify.js', modules.notify);

import { h } from 'vue';

import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
import FileSelectionActions from '@/components/experiment/FileSelectionActions.vue';
import { keepListed } from '@/utils/experimentFiles.js';
import { useJobStore } from '@/utils/jobs.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import { pathSegment } from './helpers/arbitraries.js';
import { deferredGets, never } from './helpers/async.js';
import { httpError, wsMsg } from './helpers/fixtures.js';
import {
  allowAll,
  allowOnly,
  denyAll,
  denyOnly,
} from './helpers/permissions.js';
import {
  byLabel,
  confirmDialog,
  dialogs,
  mountComponent,
  tableHeader,
  toasts,
} from './helpers/mount.js';

// a file's path in the experiment's files folder
const filePath = fc
  .array(pathSegment, { minLength: 1, maxLength: 3 })
  .map((parts) => parts.join('/'));

const row = (path, extra) => ({
  name: path.split('/').pop(),
  path,
  categories: ['Unknown'],
  ...extra,
});

const answer = (names, categories = ['Unknown']) => ({
  data: {
    files: names.map((name) => ({ name, path: name, categories })),
    total: names.length,
  },
});

// mounts `component` on exp1's page
const mountOn = (component, options) =>
  mountComponent(component, { route: '/experiment/exp1', ...options });

// a Files tab on experiment exp1, with this state
const filesTab = (state, props) =>
  mountOn(ExperimentFilesTab, {
    props: { paginateKey: 'running-files', ...props },
    data: state,
  });

// A Files tab already listing these files (rows, or paths for rows), with
// this state; asking for the list again is only recorded.
async function listing(
  files = ['a.log', 'scorch/run-0/b.json', 'c.pcap'],
  state,
) {
  files = files.map((f) => (typeof f == 'string' ? row(f) : f));
  const tab = await filesTab({ files, filesLoaded: true, ...state });
  tab.vm.filesTable.total = files.length;
  axios.get.mockReturnValue(never());
  await flushPromises();
  return tab;
}

const cells = (tab, label) =>
  tab.findAll(`tbody td[data-label="${label}"]`).map((td) => td.text());
const boxes = (tab) =>
  tab.findAll('tbody td input[type="checkbox"]').map((i) => i.element);
const rowPaths = (tab) => boxes(tab).map((box) => box.value);
const checked = (tab) =>
  boxes(tab)
    .filter((box) => box.checked)
    .map((box) => box.value);
const headerBox = (tab) => tab.find('thead input[type="checkbox"]').element;
const emptyText = (tab) => tab.find('tbody').text();
const relisted = () =>
  axios.get.mock.calls.some(([url]) => url == 'experiments/exp1/files');

// Roles, and whether each may download the experiment's files, delete them,
// and so select them.
const ROLES = [
  ['a role that may do anything', allowAll, true, true, true],
  [
    "a role that may only get exp1's files",
    allowOnly(['experiments/files', 'get', 'exp1']),
    true,
    false,
    true,
  ],
  ['a role that may not delete', denyOnly(['*', 'delete']), true, false, true],
  ['a role that may only list', allowOnly(['*', 'list']), false, false, false],
  ['a role that may do nothing', denyAll, false, false, false],
];

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
});

afterEach(() => vi.restoreAllMocks());

describe('listing files', () => {
  it('asks for the search, sort and page the table shows', async () => {
    axios.get.mockResolvedValue({ data: { files: [], total: 11 } });
    const tab = await filesTab({}, { filter: 'a&b' });

    await tab.vm.reload();
    expect(axios.get).toHaveBeenLastCalledWith('experiments/exp1/files', {
      params: { filter: 'a&b', sortCol: 'date', sortDir: 'desc' },
    });

    await flushPromises();
    await tab.find('.files-paginate input').setValue(true);
    await byLabel(tab, 'Next page').trigger('click');
    expect(axios.get).toHaveBeenLastCalledWith('experiments/exp1/files', {
      params: {
        filter: 'a&b',
        sortCol: 'date',
        sortDir: 'desc',
        pageNum: 2,
        perPage: 10,
      },
    });

    await tableHeader(tab, 'Size').trigger('click');
    expect(axios.get.mock.calls.at(-1)[1].params).toMatchObject({
      sortCol: 'size',
      sortDir: 'desc',
    });
    await tableHeader(tab, 'Size').trigger('click');
    expect(axios.get.mock.calls.at(-1)[1].params).toMatchObject({
      sortCol: 'size',
      sortDir: 'asc',
    });
  });

  it('keeps the newest answer when an older one arrives last', async () => {
    const pending = deferredGets(axios.get);
    const tab = await filesTab({}, { filter: 'o' });

    await tab.vm.reload();
    await tab.setProps({ filter: 'ol' });
    await tab.vm.reload();

    pending[1].resolve(answer(['old.pcap']));
    await flushPromises();
    pending[0].resolve(answer(['old.pcap', 'other.log']));
    await flushPromises();

    expect(cells(tab, 'Name')).toEqual(['old.pcap']);
    expect(tab.vm.filesTable.total).toBe(1);
  });

  it('does not report the failure of a replaced request', async () => {
    const pending = deferredGets(axios.get);
    const tab = await filesTab();

    await tab.vm.reload();
    await tab.vm.reload();

    pending[0].reject(new Error('Network Error'));
    pending[1].resolve(answer(['a.pcap']));
    await flushPromises();

    expect(notify.error).not.toHaveBeenCalled();
    expect(cells(tab, 'Name')).toEqual(['a.pcap']);
  });

  it('reports a list that fails to load, and stops saying it is loading', async () => {
    const err = new Error('forbidden');
    axios.get.mockRejectedValue(err);
    const tab = await filesTab();

    await tab.vm.reload();
    await flushPromises();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(emptyText(tab)).toBe('This experiment has no files yet');
  });

  it('treats a null file list as empty', async () => {
    const tab = await filesTab({ selectedFiles: ['a.log'] });
    axios.get.mockResolvedValue({ data: { files: null } });

    expect(emptyText(tab)).toBe('Loading files…');
    await tab.vm.reload();
    await flushPromises();

    expect(tab.findAll('tbody tr td[data-label="Name"]')).toHaveLength(0);
    expect(tab.find('.file-selection-actions').exists()).toBe(false);
    expect(tab.vm.filesTable.total).toBe(0);
    expect(emptyText(tab)).toBe('This experiment has no files yet');
  });

  it('does not request files without permission to list them', async () => {
    rbac.allowed = vi.fn(denyAll);
    await (await filesTab()).vm.reload();
    expect(axios.get).not.toHaveBeenCalled();
    expect(rbac.allowed).toHaveBeenCalledWith(
      'experiments/files',
      'list',
      'exp1',
    );
  });

  it('shows the picked category and offers every category seen', async () => {
    const tab = await filesTab({}, { category: 'Packet Capture' });
    axios.get.mockResolvedValueOnce({
      data: {
        files: [
          row('a.pcap', { categories: ['Packet Capture'] }),
          row('b.log'),
          row('c', { categories: null }),
        ],
        total: 3,
      },
    });

    await tab.vm.reload();
    await flushPromises();

    expect(cells(tab, 'Name')).toEqual(['a.pcap']);
    expect(tab.vm.filesTable.total).toBe(3);
    expect(tab.emitted('categories')).toEqual([
      [['Packet Capture', 'Unknown']],
    ]);
    expect(tab.vm.filesEmptyText).toBe('No files match your search');

    // categories seen before stay on offer
    axios.get.mockResolvedValueOnce(answer(['d.json'], ['Scorch Artifact']));
    await tab.vm.reload();
    await flushPromises();
    expect(tab.emitted('categories')[1]).toEqual([
      ['Packet Capture', 'Scorch Artifact', 'Unknown'],
    ]);
  });

  it('reports only searches that found files, for the search history', async () => {
    const tab = await filesTab();
    const search = async (filter, names) => {
      axios.get.mockResolvedValue(answer(names));
      await tab.setProps({ filter });
      await tab.vm.reload();
      await flushPromises();
      return tab.emitted('found')?.length ?? 0;
    };

    expect(await search('', ['a.log'])).toBe(0);
    expect(await search('a.l', ['a.log'])).toBe(1);
    expect(await search('zzz', [])).toBe(1);
  });

  it("waits for the page's new search to arrive before reloading", async () => {
    axios.get.mockResolvedValue(answer([]));
    const tab = await filesTab();

    tab.setProps({ filter: 'new' });
    const done = tab.vm.reload();
    expect(axios.get).not.toHaveBeenCalled();
    await done;

    expect(axios.get.mock.calls[0][1].params.filter).toBe('new');
  });
});

describe('selecting files', () => {
  it('keeps only the selected rows the new list still shows', async () => {
    axios.get.mockResolvedValue(answer(['a.log', 'd.log']));
    const tab = await filesTab({ selectedFiles: ['a.log', 'gone.log'] });

    await tab.vm.reload();
    await flushPromises();
    expect(checked(tab)).toEqual(['a.log']);

    // a selection the list still shows in full is kept as it is
    const kept = tab.vm.selectedFiles;
    await tab.vm.reload();
    await flushPromises();
    expect(tab.vm.selectedFiles).toBe(kept);
  });

  it('selects and clears every row shown from the header checkbox', async () => {
    const tab = await listing();
    const header = headerBox(tab);
    expect([header.checked, header.indeterminate]).toEqual([false, false]);

    await tab.find('tbody input[value="a.log"]').setValue(true);
    expect(header.indeterminate).toBe(true);

    await tab.find('thead input').setValue(true);
    expect(checked(tab)).toEqual(['a.log', 'scorch/run-0/b.json', 'c.pcap']);
    expect([header.checked, header.indeterminate]).toEqual([true, false]);

    await tab.find('thead input').setValue(false);
    expect(checked(tab)).toEqual([]);

    await tab.find('tbody input[value="c.pcap"]').setValue(true);
    await byLabel(tab, 'clear the selection').trigger('click');
    expect(checked(tab)).toEqual([]);
  });

  it('has nothing to select all in an empty list', async () => {
    const header = headerBox(await listing([]));
    expect([header.checked, header.disabled]).toEqual([false, true]);
  });

  it('keeps the selected paths still listed, in order, the same array when all are', () => {
    // selections may repeat a path or name one only in another case
    const cases = fc.uniqueArray(fc.string(), { maxLength: 8 }).chain((paths) =>
      fc.tuple(
        fc.subarray(paths).map((listed) => listed.map((p) => row(p))),
        fc.array(
          fc.constantFrom(
            ...paths,
            ...paths.map((p) => p.toUpperCase()),
            'gone.log',
          ),
          { maxLength: 10 },
        ),
      ),
    );
    const count = (list, path) => list.filter((p) => p === path).length;
    fc.assert(
      fc.property(cases, ([files, selected]) => {
        const kept = keepListed(selected, files);
        const isListed = (path) => files.some((f) => f.path === path);

        expect(kept.every(isListed)).toBe(true);
        // kept is selected with some paths dropped, in the same order
        let i = 0;
        for (const path of selected) if (path === kept[i]) i++;
        expect(i).toBe(kept.length);
        for (const path of selected.filter(isListed)) {
          expect(count(kept, path)).toBe(count(selected, path));
        }
        expect(kept === selected).toBe(kept.length === selected.length);
      }),
    );
  });
});

describe('opening a file', () => {
  it('shows plain text in the viewer, with the page waiting meanwhile', async () => {
    const tab = await listing([row('scorch/a b.json', { plainText: true })]);
    const pending = deferredGets(axios.get);

    await byLabel(tab, 'View a b.json').trigger('click');
    expect(tab.emitted('waiting')).toEqual([[true]]);
    pending[0].resolve({ data: '{"a":1}' });
    await flushPromises();

    expect(axios.get).toHaveBeenCalledWith(
      'experiments/exp1/files/a%20b.json',
      {
        params: { path: 'scorch/a b.json' },
        headers: { Accept: 'text/plain' },
        responseType: 'text',
      },
    );
    expect(tab.find('.modal.is-active .modal-card-title').text()).toBe(
      'scorch/a b.json',
    );
    expect(tab.find('.file-viewer').element.value).toBe('{\n  "a": 1\n}');
    expect(tab.emitted('waiting')).toEqual([[true], [false]]);

    await tab.find('.modal-card-foot button').trigger('click');
    expect(tab.find('.modal').element.style.display).toBe('none');
    expect(tab.find('.modal-card-title').text()).toBe('');
    expect(tab.find('.file-viewer').element.value).toBe('');
  });

  it.each([
    [
      'indents JSON, whatever the case of its name',
      'run.JSON',
      '{"a":[1]}',
      '{\n  "a": [\n    1\n  ]\n}',
    ],
    ['shows other files as they are', 'out.log', '{"a":1}', '{"a":1}'],
    ['shows invalid JSON as it is', 'bad.json', '{"a":', '{"a":'],
    ['shows nothing for a missing body', 'x.json', undefined, ''],
  ])('%s', async (_, name, body, contents) => {
    const tab = await listing([row(name, { plainText: true })]);
    axios.get.mockResolvedValue({ data: body });

    await byLabel(tab, `View ${name}`).trigger('click');
    await flushPromises();
    expect(tab.find('.file-viewer').element.value).toBe(contents);
  });

  it('reports a file it cannot read, and stops the page waiting', async () => {
    const tab = await listing([row('gone.log', { plainText: true })]);
    const err = new Error('not found');
    axios.get.mockRejectedValue(err);

    await byLabel(tab, 'View gone.log').trigger('click');
    await flushPromises();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(tab.find('.modal.is-active').exists()).toBe(false);
    expect(tab.emitted('waiting')).toEqual([[true], [false]]);
  });

  it('downloads a file with the token in the query, its name escaped', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const tab = await listing(['logs/run #1?.log']);

    await byLabel(tab, 'Download run #1?.log').trigger('click');
    expect(open).toHaveBeenCalledWith(
      '/api/v1/experiments/exp1/files/run%20%231%3F.log?path=logs%2Frun+%231%3F.log&token=t',
      '_blank',
    );
  });

  it('downloads any file by a URL its name and path read back from intact', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    await fc.assert(
      fc.asyncProperty(filePath, async (path) => {
        const tab = await listing([path]);
        // by attribute value: happy-dom refuses a selector escaping a quote
        const label = `Download ${row(path).name}`;
        await tab
          .findAll('button')
          .find((b) => b.attributes('aria-label') === label)
          .trigger('click');

        const url = new URL(open.mock.lastCall[0], 'http://phenix.test/');
        const [name, ...dir] = url.pathname.split('/').toReversed();
        expect(dir.toReversed().join('/')).toBe(
          '/api/v1/experiments/exp1/files',
        );
        expect(decodeURIComponent(name)).toBe(row(path).name);
        expect(Object.fromEntries(url.searchParams)).toEqual({
          path,
          token: 't',
        });
        tab.unmount();
      }),
      { numRuns: 20 },
    );
  });

  it('opens capture files on the WebShark page', async () => {
    store.features = ['webshark'];
    const tab = await listing([
      'caps/web_0.pcap',
      'old.PCAPNG.gz',
      'notes.txt',
      row('run.pcap', { isDir: true }),
    ]);
    const webShark = (name) => byLabel(tab, `Open ${name} in WebShark`);

    expect(webShark('web_0.pcap').attributes('href')).toBe(
      '/packets?exp=exp1&file=caps/web_0.pcap',
    );
    expect(webShark('old.PCAPNG.gz').exists()).toBe(true);

    await tab.find('td[data-label="Name"] .is-clickable div').trigger('click');
    await flushPromises();
    expect(tab.router.currentRoute.value).toMatchObject({
      name: 'webshark',
      query: { exp: 'exp1', file: 'caps/web_0.pcap' },
    });

    // not other files, or a directory named like a capture
    expect(webShark('notes.txt').exists()).toBe(false);
    expect(webShark('run.pcap').exists()).toBe(false);
  });

  it.each([
    ['WebShark is not installed', ['tunneler-download'], allowAll],
    ['the server has not said what it has installed', null, allowAll],
    [
      'the role may not get the files',
      ['webshark'],
      denyOnly(['experiments/files', 'get', 'exp1']),
    ],
  ])('opens nothing in WebShark when %s', async (_, features, allowed) => {
    store.features = features;
    rbac.allowed = allowed;
    const tab = await listing(['a.pcap']);
    expect(byLabel(tab, 'Open a.pcap in WebShark').exists()).toBe(false);
    expect(tab.find('td[data-label="Name"] .is-clickable').exists()).toBe(
      false,
    );
  });
});

describe('deleting a file', () => {
  it('names the file in the confirmation, escaped', async () => {
    const tab = await listing(['scorch/<b>x</b>.log']);
    await tab.find('.file-delete').trigger('click');

    const [dialog] = dialogs();
    const confirm = dialog.querySelector('footer .button:last-child');
    expect(confirm.classList).toContain('is-danger');
    expect(confirm.textContent).toBe('Delete');
    const message = dialog.querySelector('.media-content').innerHTML;
    expect(message).toContain('&lt;b&gt;x&lt;/b&gt;.log');
    expect(message).toContain('scorch/&lt;b&gt;x&lt;/b&gt;.log');
    expect(message).not.toContain('<b>x</b>');
  });

  it('deletes after confirming, spinning until done, then relists', async () => {
    const request = Promise.withResolvers();
    axios.delete.mockReturnValue(request.promise);
    const tab = await listing(undefined, {
      selectedFiles: ['scorch/run-0/b.json', 'a.log'],
    });

    await byLabel(tab, 'Delete b.json').trigger('click');
    await confirmDialog();

    expect(axios.delete).toHaveBeenCalledWith('experiments/exp1/files/b.json', {
      params: { path: 'scorch/run-0/b.json' },
    });
    expect(byLabel(tab, 'Delete b.json').classes()).toContain('is-loading');

    request.resolve({ status: 204 });
    await flushPromises();

    expect(tab.find('.file-delete.is-loading').exists()).toBe(false);
    expect(rowPaths(tab)).toEqual(['a.log', 'c.pcap']);
    expect(tab.vm.filesTable.total).toBe(2);
    expect(checked(tab)).toEqual(['a.log']);
    expect(toasts()[0].type).toBe('is-success');
    expect(relisted()).toBe(true);
  });

  it('keeps the row and shows the error when the delete fails', async () => {
    const err = new Error('capture still in progress');
    axios.delete.mockRejectedValue(err);
    const tab = await listing();

    await byLabel(tab, 'Delete c.pcap').trigger('click');
    await confirmDialog();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(rowPaths(tab)).toHaveLength(3);
    expect(tab.find('.file-delete.is-loading').exists()).toBe(false);
    expect(relisted()).toBe(false);
  });

  it('does not delete the same file twice at once', async () => {
    axios.delete.mockReturnValue(never());
    const tab = await listing();
    for (let i = 0; i < 2; i++) {
      await byLabel(tab, 'Delete a.log').trigger('click');
      await confirmDialog();
    }
    expect(axios.delete).toHaveBeenCalledTimes(1);
  });
});

describe('deleting the selected files', () => {
  // the tab listing the files, these selected, its toolbar's Delete clicked
  async function deleting(selectedFiles, paths) {
    const tab = await listing(paths, { selectedFiles });
    await tab.find('.file-delete-selected').trigger('click');
    return tab;
  }

  it('confirms once with the count, then deletes them together', async () => {
    axios.post.mockResolvedValue({
      data: { deleted: ['a.log', 'c.pcap'], failed: [] },
    });
    const tab = await deleting(['a.log', 'c.pcap']);

    expect(dialogs()).toHaveLength(1);
    expect(dialogs()[0].textContent).toContain('2 selected files');
    expect(dialogs()[0].querySelector('footer .is-danger').textContent).toBe(
      'Delete 2 Files',
    );

    dialogs()[0].querySelector('footer .is-danger').click();
    await tab.vm.$nextTick();
    expect(tab.find('.file-delete-selected').classes()).toContain('is-loading');
    await flushPromises();

    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/files/delete', {
      paths: ['a.log', 'c.pcap'],
    });
    expect(tab.find('.file-selection-actions').exists()).toBe(false);
    expect(cells(tab, 'Name')).toEqual(['b.json']);
    expect(rowPaths(tab)).toEqual(['scorch/run-0/b.json']);
    expect(toasts()[0].text).toBe('Deleted 2 files');
    expect(notify.show).not.toHaveBeenCalled();
    expect(relisted()).toBe(true);
  });

  it('asks nothing when nothing is selected', async () => {
    const tab = await listing();
    tab.vm.confirmDeleteSelectedFiles();
    expect(dialogs()).toHaveLength(0);
  });

  it('reports the files it could not delete, which stay selected', async () => {
    axios.post.mockResolvedValue({
      data: {
        deleted: ['a.log'],
        failed: [
          { path: 'c.pcap', error: 'capture already exists: c.pcap' },
          { path: 'scorch/run-0/b.json', error: 'file not found' },
        ],
      },
    });
    const tab = await deleting(['a.log', 'c.pcap', 'scorch/run-0/b.json']);
    await confirmDialog();

    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain('Deleted 1 of 3 files; 2 failed');
    expect(noticeText()).toContain('c.pcap: capture already exists: c.pcap');
    expect(noticeText()).toContain('scorch/run-0/b.json: file not found');
    expect(checked(tab)).toEqual(['scorch/run-0/b.json', 'c.pcap']);
    expect(rowPaths(tab)).toHaveLength(2);
    expect(toasts()).toHaveLength(0);
  });

  it('drops the rows the server deleted and keeps the rest it was asked for selected', async () => {
    // the files listed, those selected, and those of them the server deletes
    const cases = fc
      .uniqueArray(filePath, { minLength: 1, maxLength: 6 })
      .chain((paths) =>
        fc.tuple(
          fc.constant(paths),
          fc
            .subarray(paths, { minLength: 1 })
            .chain((selected) =>
              fc.tuple(fc.constant(selected), fc.subarray(selected)),
            ),
        ),
      );
    await fc.assert(
      fc.asyncProperty(cases, async ([paths, [selected, deleted]]) => {
        resetMocks();
        document.body.innerHTML = '';
        const left = (path) => !deleted.includes(path);
        const failed = selected
          .filter(left)
          .map((path) => ({ path, error: 'busy' }));
        axios.post.mockResolvedValue({ data: { deleted, failed } });
        const tab = await deleting(selected, paths);
        await confirmDialog();

        expect(rowPaths(tab)).toEqual(paths.filter(left));
        expect(checked(tab)).toEqual(
          paths.filter((path) => selected.includes(path) && left(path)),
        );
        expect(toasts()).toHaveLength(failed.length ? 0 : 1);
        expect(notices.open).toHaveBeenCalledTimes(failed.length ? 1 : 0);
        tab.unmount();
      }),
      { numRuns: 10 },
    );
  });

  it('names the first eight failures and counts the rest', async () => {
    const paths = Array.from({ length: 12 }, (_, i) => `f${i}.log`);
    axios.post.mockResolvedValue({
      data: {
        deleted: [],
        failed: paths.map((path) => ({ path, error: 'busy' })),
      },
    });
    await deleting(paths, paths);
    await confirmDialog();

    expect(noticeText()).toContain('f7.log: busy');
    expect(noticeText()).not.toContain('f8.log: busy');
    expect(noticeText()).toContain('and 4 more');
  });

  it('shows the error and relists when the request fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const tab = await deleting(['a.log']);
    await confirmDialog();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(rowPaths(tab)).toHaveLength(3);
    expect(tab.find('.file-delete-selected').classes()).not.toContain(
      'is-loading',
    );
    expect(relisted()).toBe(true);
  });
});

describe('downloading the selected files', () => {
  // the server's archive job for the paths, as it ends
  const archiveJob = (fields = {}) => ({
    id: 'j1',
    kind: 'files.archive',
    experiment: 'exp1',
    status: 'succeeded',
    items: [
      { name: 'a.log', status: 'succeeded' },
      { name: 'c.pcap', status: 'succeeded' },
    ],
    artifact: { name: 'exp1-files.zip', size: 10 },
    revision: 3,
    ...fields,
  });

  // the store's artifact downloads, which need a document
  const downloads = () =>
    vi.spyOn(useJobStore(), 'downloadArtifact').mockImplementation(() => {});

  // the tab with these files selected, and its toolbar's Download button
  async function selected(selectedFiles) {
    const tab = await listing(undefined, { selectedFiles });
    return { tab, download: tab.find('.file-download-selected') };
  }

  it('builds the archive in a job and downloads it when done', async () => {
    const saved = downloads();
    axios.post.mockResolvedValue({
      data: archiveJob({ status: 'running', artifact: undefined, revision: 1 }),
    });
    const { tab, download } = await selected(['a.log', 'c.pcap']);
    axios.get.mockResolvedValue({ data: archiveJob() });

    await download.trigger('click');
    expect(download.attributes('disabled')).toBeDefined();
    expect(tab.vm.filesBusy).toBe(true);
    await flushPromises();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/exp1/files/archive',
      { paths: ['a.log', 'c.pcap'] },
      undefined,
    );
    expect(tab.vm.archiveJob.status).toBe('running');

    // the job's websocket update says it finished
    useJobStore().apply(wsMsg('job', 'j1', 'update', archiveJob()));
    await flushPromises();

    expect(saved).toHaveBeenCalledWith(expect.objectContaining({ id: 'j1' }));
    expect(download.attributes('disabled')).toBeUndefined();
  });

  it('leaves an incomplete archive for an explicit click', async () => {
    const saved = downloads();
    const incomplete = archiveJob({
      status: 'partial',
      artifact: {
        name: 'exp1-files-incomplete.zip',
        size: 5,
        incomplete: true,
      },
    });
    axios.post.mockResolvedValue({ data: incomplete });
    const { tab, download } = await selected(['a.log', 'c.pcap']);
    axios.get.mockResolvedValue({ data: incomplete });

    await download.trigger('click');
    await flushPromises();

    expect(saved).not.toHaveBeenCalled();
    expect(tab.vm.archiveJob.artifact.incomplete).toBe(true);
  });

  it('sums up an archive that could not be built, and one left behind downloads nothing', async () => {
    const saved = downloads();
    const failed = archiveJob({
      status: 'failed',
      artifact: undefined,
      items: [{ name: 'a.log', status: 'failed', error: 'not found' }],
    });
    axios.post.mockResolvedValue({ data: failed });
    const { tab, download } = await selected(['a.log']);
    axios.get.mockResolvedValue({ data: failed });

    await download.trigger('click');
    await flushPromises();
    expect(noticeText()).toContain('a.log: not found');

    // a tab the user left opens nothing
    axios.post.mockResolvedValue({ data: archiveJob() });
    axios.get.mockResolvedValue({ data: archiveJob() });
    download.trigger('click');
    tab.unmount();
    await flushPromises();
    expect(saved).not.toHaveBeenCalled();
  });

  it('shows a request it cannot start', async () => {
    const err = httpError(413, 'download too large');
    axios.post.mockRejectedValue(err);
    const { download } = await selected(['a.log']);

    await download.trigger('click');
    await flushPromises();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(download.attributes('disabled')).toBeUndefined();
  });

  it('waits for a download or delete in progress', async () => {
    const tab = await listing(undefined, {
      selectedFiles: ['a.log'],
      filesBulkDeleting: true,
    });
    await tab.vm.downloadSelectedFiles();
    await tab.vm.deleteSelectedFiles();
    expect(axios.post).not.toHaveBeenCalled();
  });
});

describe('rendering', () => {
  // what each button or link in a row's Actions cell is for, in order
  const actions = (tab, i) =>
    tab
      .findAll('tbody td[data-label="Actions"]')
      [i].findAll('a, button')
      .map((el) => {
        if (el.attributes('aria-hidden') == 'true') return '(space)';
        const label = el.attributes('aria-label');
        const href = el.attributes('href');
        return href ? `${label} -> ${href}` : label;
      });

  it('has no toolbar while nothing is checked and one page holds all', async () => {
    const tab = await filesTab();
    expect(tab.find('.files-bar').exists()).toBe(false);
    expect(tab.text()).not.toContain('Paginate');
    expect(tab.find('.b-table').exists()).toBe(true);
    expect(emptyText(tab)).toBe('Loading files…');
  });

  it('offers Paginate once the files fill more than a page', async () => {
    const tab = await listing([]);
    tab.vm.filesTable.total = 11;
    await tab.vm.$nextTick();
    expect(tab.find('.files-bar').exists()).toBe(true);
    expect(tab.find('.files-bar').text()).toContain('Paginate');
    expect(tab.find('.file-selection-actions').exists()).toBe(false);
  });

  it("keeps the page's Paginate setting under its own key", async () => {
    localStorage.setItem('phenix.paginate.running-files', 'true');
    const tab = await listing([]);
    tab.vm.filesTable.total = 11;
    await tab.vm.$nextTick();
    expect(tab.find('.files-paginate input').element.checked).toBe(true);
    expect(tab.find('.pagination').exists()).toBe(true);
  });

  it('shows the selection toolbar while files are checked', async () => {
    const tab = await listing(['a.log'], { selectedFiles: ['a.log'] });
    expect(tab.find('.file-selection-actions').exists()).toBe(true);
    expect(tab.find('.file-download-selected').text()).toBe('Download (1)');
    expect(tab.find('.file-delete-selected').text()).toBe('Delete (1)');
  });

  it('gives each row the button that opens it, then download and delete', async () => {
    store.features = ['webshark'];
    const tab = await listing([
      'caps/a.pcap',
      row('notes.txt', { plainText: true }),
      'disk.img',
    ]);

    expect(actions(tab, 0)).toEqual([
      'Open a.pcap in WebShark -> /packets?exp=exp1&file=caps/a.pcap',
      'Download a.pcap',
      'Delete a.pcap',
    ]);
    expect(
      byLabel(tab, 'Open a.pcap in WebShark')
        .find('[data-icon="shark-fin"]')
        .exists(),
    ).toBe(true);
    expect(actions(tab, 1)).toEqual([
      'View notes.txt',
      'Download notes.txt',
      'Delete notes.txt',
    ]);
    // an unseen button keeps the download buttons in line
    expect(actions(tab, 2)).toEqual([
      '(space)',
      'Download disk.img',
      'Delete disk.img',
    ]);

    // the name opens the file the same way
    const names = tab
      .findAll('tbody td[data-label="Name"]')
      .map((td) => td.findAll('.tooltip-content, .is-clickable'))
      .map((parts) => parts.map((part) => part.text()));
    expect(names).toEqual([
      ['Open in WebShark', 'a.pcap'],
      ['view file', 'notes.txt'],
      [],
    ]);
    expect(tab.find('tbody td[data-label="Path"]').text()).toContain(
      '/phenix/images/exp1/files/caps/a.pcap',
    );
  });

  it.each(ROLES)(
    "leaves out the checkboxes, views, downloads and deletes %s can't use",
    async (_, role, download, del, select) => {
      rbac.allowed = role;
      const tab = await listing(['a.log', row('b.txt', { plainText: true })]);
      expect(actions(tab, 0)).toEqual([
        '(space)',
        ...(download ? ['Download a.log'] : []),
        ...(del ? ['Delete a.log'] : []),
      ]);
      expect(actions(tab, 1)).toEqual([
        download ? 'View b.txt' : '(space)',
        ...(download ? ['Download b.txt'] : []),
        ...(del ? ['Delete b.txt'] : []),
      ]);
      // the name opens the file only for a role that may get it
      expect(
        tab.findAll('tbody td[data-label="Name"] .is-clickable').length,
      ).toBe(download ? 1 : 0);
      expect(rowPaths(tab).length > 0).toBe(select);
    },
  );

  it('spins the delete button at work and holds the others', async () => {
    const toolbar = async (props) =>
      (await mountOn(FileSelectionActions, { props: { count: 2, ...props } }))
        .findAll('button')
        .map((b) => ({
          loading: b.classes('is-loading'),
          disabled: b.element.disabled,
        }));
    const idle = { loading: false, disabled: false };
    const held = { loading: false, disabled: true };
    const can = { canDownload: true, canDelete: true };

    expect(await toolbar(can)).toEqual([idle, idle, idle]);
    // the archive job's progress shows instead of a spinner
    expect(await toolbar({ ...can, downloading: true })).toEqual([
      held,
      held,
      held,
    ]);
    expect(await toolbar({ ...can, deleting: true })).toEqual([
      held,
      { loading: true, disabled: false },
      held,
    ]);
    // only the clear button without permission to download or delete
    expect(await toolbar({})).toEqual([idle]);
  });

  it("shows the archive job's progress", async () => {
    const toolbar = await mountOn(FileSelectionActions, {
      props: {
        count: 2,
        canDownload: true,
        downloading: true,
        archive: {
          id: 'j1',
          title: 'Archive 2 files of exp1',
          kind: 'files.archive',
          status: 'running',
          cancelable: true,
          progress: { done: 1, total: 2, bytes: 5, bytesTotal: 10 },
          items: [],
        },
      },
    });
    expect(toolbar.text()).toContain('1 of 2 files done');
    expect(byLabel(toolbar, 'Cancel Archive 2 files of exp1').exists()).toBe(
      true,
    );
  });
});

describe('the experiment pages', () => {
  const pages = [
    [
      'StoppedExperiment',
      StoppedExperiment,
      'stopped-files',
      { searchName: 'web-server' },
    ],
    [
      'RunningExperiment',
      RunningExperiment,
      'running-files',
      { search: { filter: 'web-server' } },
    ],
  ];

  // the page on exp1 with its Files tab open, and what passed between them
  async function mountPage(view, state) {
    // the page's own lists never arrive
    axios.get.mockReturnValue(never());

    const tabs = [];
    const FilesTab = {
      props: ExperimentFilesTab.props,
      emits: ExperimentFilesTab.emits,
      created() {
        tabs.push({ ...this.$props });
        this.$emit('categories', ['Packet Capture', 'Unknown']);
        this.$emit('found');
        this.$emit('waiting', true);
      },
      methods: { reload: vi.fn() },
      render: () => h('div'),
    };
    const page = await mountOn(view, {
      data: {
        activeTab: 1,
        experiment: { name: 'exp1', vms: [] },
        fileCategory: 'Packet Capture',
        ...state,
      },
      stubs: { ExperimentFilesTab: FilesTab, RouterLink: RouterLinkStub },
    });
    return { page, tabs, reload: FilesTab.methods.reload };
  }

  it.each(pages)(
    '%s passes the tab its search and category, and takes its news',
    async (_, view, paginateKey, search) => {
      const { page, tabs } = await mountPage(view, search);

      expect(tabs).toEqual([
        { paginateKey, filter: 'web-server', category: 'Packet Capture' },
      ]);
      const categories = page.find(
        'select[aria-label="Filter files by category"]',
      );
      expect(categories.findAll('option').map((o) => o.text())).toEqual([
        'Packet Capture',
        'Unknown',
      ]);
      expect(
        page.findComponent({ name: 'BAutocomplete' }).props('data'),
      ).toEqual(['web-server']);
      expect(page.find('.loading-overlay.is-active').exists()).toBe(true);
      // the tab holds the file list, not the page
      expect(page.vm.files).toBeUndefined();
    },
  );

  it.each(pages)(
    '%s reloads the tab when the category changes',
    async (_, view) => {
      const { page, reload } = await mountPage(view, {});
      await page
        .find('select[aria-label="Filter files by category"]')
        .setValue('Unknown');
      expect(page.vm.fileCategory).toBe('Unknown');
      expect(reload).toHaveBeenCalled();
    },
  );
});
