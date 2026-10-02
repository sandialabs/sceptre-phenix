// The experiment pages' Files tab (ExperimentFilesTab.vue): listing, opening,
// selecting, downloading and deleting the experiment's files, and how the
// running and stopped experiment pages hold it.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, rbac, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/experimentMocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif', modules.errorNotif);
vi.mock('@/store.js', modules.store);
// the experiment pages around the tab
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);

const saver = vi.hoisted(() => ({ saveAs: vi.fn() }));
vi.mock('file-saver', () => ({ default: saver }));

import { h } from 'vue';

import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
import FileSelectionActions from '@/components/experiment/FileSelectionActions.vue';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import { deferredGets, flush } from './helpers/async.js';
import { makeContext } from './helpers/context.js';
import { renderSSR, stubBrowser, tableRows, textOf } from './helpers/render.js';
import { memoryStorage } from './helpers/storage.js';

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

// a Files tab on experiment exp1, with a dialog that records what it asks
function filesTab(fields = {}) {
  const tab = makeContext(ExperimentFilesTab, {
    paginateKey: 'test-files',
    filter: '',
    category: null,
    $route: { params: { id: 'exp1' } },
    $router: { push: vi.fn() },
    $emit: vi.fn(),
    $nextTick: () => Promise.resolve(),
    dialogs: [],
    toasts: [],
    ...fields,
  });
  tab.$buefy = {
    dialog: { confirm: (opts) => tab.dialogs.push(opts) },
    toast: { open: (opts) => tab.toasts.push(opts) },
  };
  return tab;
}

// a Files tab already listing these paths; asking for the list again is
// only recorded
function listing(paths = ['a.log', 'scorch/run-0/b.json', 'c.pcap']) {
  const tab = filesTab({
    files: paths.map((p) => row(p)),
    updateFiles: vi.fn(),
  });
  tab.filesTable.total = paths.length;
  return tab;
}

const emitted = (tab, event) =>
  tab.$emit.mock.calls.filter(([name]) => name == event).map(([, v]) => v);

beforeEach(() => {
  resetMocks();
  saver.saveAs.mockReset();
});

afterEach(() => vi.unstubAllGlobals());

describe('listing files', () => {
  it('asks for the search, sort and page the table shows', () => {
    axios.get.mockResolvedValue(answer([]));
    const tab = filesTab({ filter: 'a&b' });

    tab.updateFiles();
    expect(axios.get).toHaveBeenLastCalledWith('experiments/exp1/files', {
      params: { filter: 'a&b', sortCol: 'date', sortDir: 'desc' },
    });

    tab.filesTable.isPaginated = true;
    tab.onFilesPageChange(2);
    expect(axios.get).toHaveBeenLastCalledWith('experiments/exp1/files', {
      params: {
        filter: 'a&b',
        sortCol: 'date',
        sortDir: 'desc',
        pageNum: 2,
        perPage: 10,
      },
    });

    tab.onFilesSort('size', 'asc');
    expect(axios.get.mock.calls.at(-1)[1].params).toMatchObject({
      sortCol: 'size',
      sortDir: 'asc',
    });
  });

  it('keeps the newest answer when an older one arrives last', async () => {
    const pending = deferredGets(axios.get);
    const tab = filesTab({ filter: 'o' });

    tab.updateFiles();
    tab.filter = 'ol';
    tab.updateFiles();

    pending[1].resolve(answer(['old.pcap']));
    await flush();
    pending[0].resolve(answer(['old.pcap', 'other.log']));
    await flush();

    expect(tab.files.map((f) => f.name)).toEqual(['old.pcap']);
    expect(tab.filesTable.total).toBe(1);
  });

  it('does not report the failure of a replaced request', async () => {
    const pending = deferredGets(axios.get);
    const tab = filesTab();

    tab.updateFiles();
    tab.updateFiles();

    pending[0].reject(new Error('Network Error'));
    pending[1].resolve(answer(['a.pcap']));
    await flush();

    expect(notify.error).not.toHaveBeenCalled();
    expect(tab.files.map((f) => f.name)).toEqual(['a.pcap']);
  });

  it('reports a list that fails to load, and stops saying it is loading', async () => {
    const err = new Error('forbidden');
    axios.get.mockRejectedValue(err);
    const tab = filesTab();

    tab.updateFiles();
    await flush();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(tab.filesLoaded).toBe(true);
  });

  it('treats a null file list as empty', async () => {
    const tab = filesTab();
    tab.selectedFiles = ['a.log'];
    axios.get.mockResolvedValue({ data: { files: null } });

    expect(tab.filesEmptyText).toBe('Loading files…');
    tab.updateFiles();
    await flush();

    expect(tab.files).toEqual([]);
    expect(tab.selectedFiles).toEqual([]);
    expect(tab.filesTable.total).toBe(0);
    expect(tab.filesLoaded).toBe(true);
    expect(tab.filesEmptyText).toBe('This experiment has no files yet');
  });

  it('does not request files without permission to list them', () => {
    rbac.allowed = vi.fn(() => false);
    filesTab().updateFiles();
    expect(axios.get).not.toHaveBeenCalled();
    expect(rbac.allowed).toHaveBeenCalledWith(
      'experiments/files',
      'list',
      'exp1',
    );
  });

  it('shows the picked category and offers every category seen', async () => {
    const tab = filesTab({ category: 'Packet Capture' });
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

    tab.updateFiles();
    await flush();

    expect(tab.files.map((f) => f.name)).toEqual(['a.pcap']);
    expect(tab.filesTable.total).toBe(3);
    expect(emitted(tab, 'categories')).toEqual([['Packet Capture', 'Unknown']]);
    expect(tab.filesEmptyText).toBe('No files match your search');

    // categories seen before stay on offer
    axios.get.mockResolvedValueOnce(answer(['d.json'], ['Scorch Artifact']));
    tab.updateFiles();
    await flush();
    expect(emitted(tab, 'categories')[1]).toEqual([
      'Packet Capture',
      'Scorch Artifact',
      'Unknown',
    ]);
  });

  it('reports only searches that found files, for the search history', async () => {
    const tab = filesTab();
    axios.get.mockResolvedValue(answer(['a.log']));
    tab.updateFiles();
    await flush();
    expect(emitted(tab, 'found')).toHaveLength(0);

    tab.filter = 'a.l';
    tab.updateFiles();
    await flush();
    expect(emitted(tab, 'found')).toHaveLength(1);

    axios.get.mockResolvedValue(answer([]));
    tab.filter = 'zzz';
    tab.updateFiles();
    await flush();
    expect(emitted(tab, 'found')).toHaveLength(1);
  });

  it("waits for the page's new search to arrive before reloading", async () => {
    axios.get.mockResolvedValue(answer([]));
    const tick = Promise.withResolvers();
    const tab = filesTab({ $nextTick: () => tick.promise });

    const done = tab.reload();
    expect(axios.get).not.toHaveBeenCalled();
    tab.filter = 'new';
    tick.resolve();
    await done;

    expect(axios.get.mock.calls[0][1].params.filter).toBe('new');
  });
});

describe('selecting files', () => {
  it('keeps only the selected rows the new list still shows', async () => {
    axios.get.mockResolvedValue(answer(['a.log', 'd.log']));
    const tab = filesTab();
    tab.selectedFiles = ['a.log', 'gone.log'];

    tab.updateFiles();
    await flush();
    expect(tab.selectedFiles).toEqual(['a.log']);

    // a selection the list still shows in full is kept as it is
    const kept = tab.selectedFiles;
    tab.updateFiles();
    await flush();
    expect(tab.selectedFiles).toBe(kept);
  });

  it('selects and clears every row shown from the header checkbox', () => {
    const tab = listing();
    expect(tab.allFilesSelected).toBe(false);
    expect(tab.someFilesSelected).toBe(false);

    tab.selectedFiles = ['a.log'];
    expect(tab.someFilesSelected).toBe(true);

    tab.allFilesSelected = true;
    expect(tab.selectedFiles).toEqual([
      'a.log',
      'scorch/run-0/b.json',
      'c.pcap',
    ]);
    expect(tab.allFilesSelected).toBe(true);
    expect(tab.someFilesSelected).toBe(false);

    tab.allFilesSelected = false;
    expect(tab.selectedFiles).toEqual([]);

    tab.selectedFiles = ['c.pcap'];
    tab.clearFileSelection();
    expect(tab.selectedFiles).toEqual([]);
  });

  it('has nothing to select all in an empty list', () => {
    expect(listing([]).allFilesSelected).toBe(false);
  });

  it('offers deletes only to roles allowed to delete files', () => {
    rbac.allowed = (resource, verb, name) =>
      resource == 'experiments/files' && verb == 'get' && name == 'exp1';
    const tab = listing();
    expect(tab.canDownloadFiles).toBe(true);
    expect(tab.canDeleteFiles).toBe(false);
    expect(tab.filesSelectable).toBe(true);

    rbac.allowed = () => false;
    expect(tab.filesSelectable).toBe(false);
  });
});

describe('opening a file', () => {
  it('shows plain text in the viewer, with the page waiting meanwhile', async () => {
    axios.get.mockResolvedValue({ data: '{"a":1}' });
    const tab = filesTab();

    tab.viewFile(row('scorch/a b.json'));
    expect(emitted(tab, 'waiting')).toEqual([true]);
    await flush();

    expect(axios.get).toHaveBeenCalledWith(
      'experiments/exp1/files/a%20b.json',
      {
        params: { path: 'scorch/a b.json' },
        headers: { Accept: 'text/plain' },
        responseType: 'text',
      },
    );
    expect(tab.fileViewerModal).toEqual({
      active: true,
      title: 'scorch/a b.json',
      contents: '{\n  "a": 1\n}',
    });
    expect(emitted(tab, 'waiting')).toEqual([true, false]);

    tab.resetFileViewerModal();
    expect(tab.fileViewerModal).toEqual({
      active: false,
      title: null,
      contents: null,
    });
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
    axios.get.mockResolvedValue({ data: body });
    const tab = filesTab();

    tab.viewFile(row(name));
    await flush();
    expect(tab.fileViewerModal.contents).toBe(contents);
  });

  it('reports a file it cannot read, and stops the page waiting', async () => {
    const err = new Error('not found');
    axios.get.mockRejectedValue(err);
    const tab = filesTab();

    tab.viewFile(row('gone.log'));
    await flush();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(tab.fileViewerModal.active).toBe(false);
    expect(emitted(tab, 'waiting')).toEqual([true, false]);
  });

  it('downloads a file with the token in the query, its name escaped', () => {
    const open = vi.fn();
    vi.stubGlobal('window', { open });
    filesTab().downloadFile(row('logs/run #1?.log'));
    expect(open).toHaveBeenCalledWith(
      '/api/v1/experiments/exp1/files/run%20%231%3F.log?path=logs%2Frun+%231%3F.log&token=t',
      '_blank',
    );
  });

  it('opens capture files on the WebShark page', () => {
    store.features = ['webshark'];
    const tab = filesTab();
    const link = {
      name: 'webshark',
      query: { exp: 'exp1', file: 'caps/web_0.pcap' },
    };
    expect(tab.webSharkLink(row('caps/web_0.pcap'))).toEqual(link);
    expect(tab.webSharkLink(row('old.PCAPNG.gz'))).not.toBe(null);

    tab.openFileInWebShark(row('caps/web_0.pcap'));
    expect(tab.$router.push).toHaveBeenCalledWith(link);

    // not other files, or a directory named like a capture
    expect(tab.webSharkLink(row('notes.txt'))).toBe(null);
    expect(tab.webSharkLink(row('run.pcap', { isDir: true }))).toBe(null);
  });

  it.each([
    ['WebShark is not installed', ['tunneler-download'], () => true],
    ['the server has not said what it has installed', null, () => true],
    [
      'the role may not get the files',
      ['webshark'],
      (resource, verb, name) =>
        !(resource == 'experiments/files' && verb == 'get' && name == 'exp1'),
    ],
  ])('opens nothing in WebShark when %s', (_, features, allowed) => {
    store.features = features;
    rbac.allowed = allowed;
    const tab = filesTab();
    expect(tab.canOpenInWebShark).toBe(false);
    expect(tab.webSharkLink(row('a.pcap'))).toBe(null);
  });
});

describe('deleting a file', () => {
  it('names the file in the confirmation, escaped', () => {
    const tab = listing(['scorch/<b>x</b>.log']);
    tab.confirmDeleteFile(tab.files[0]);
    const [dialog] = tab.dialogs;
    expect(dialog.type).toBe('is-danger');
    expect(dialog.confirmText).toBe('Delete');
    expect(dialog.message).toContain('&lt;b&gt;x&lt;/b&gt;.log');
    expect(dialog.message).toContain('scorch/&lt;b&gt;x&lt;/b&gt;.log');
    expect(dialog.message).not.toContain('<b>x</b>');
  });

  it('deletes after confirming, spinning until done, then relists', async () => {
    const request = Promise.withResolvers();
    axios.delete.mockReturnValue(request.promise);
    const tab = listing();
    tab.selectedFiles = ['scorch/run-0/b.json', 'a.log'];

    tab.confirmDeleteFile(tab.files[1]);
    tab.dialogs[0].onConfirm();

    expect(axios.delete).toHaveBeenCalledWith('experiments/exp1/files/b.json', {
      params: { path: 'scorch/run-0/b.json' },
    });
    expect(tab.isDeletingFile(tab.files[1])).toBe(true);

    request.resolve({ status: 204 });
    await flush();

    expect(tab.deletingFiles).toEqual([]);
    expect(tab.files.map((f) => f.path)).toEqual(['a.log', 'c.pcap']);
    expect(tab.filesTable.total).toBe(2);
    expect(tab.selectedFiles).toEqual(['a.log']);
    expect(tab.toasts[0].type).toBe('is-success');
    expect(tab.updateFiles).toHaveBeenCalled();
  });

  it('keeps the row and shows the error when the delete fails', async () => {
    const err = new Error('capture still in progress');
    axios.delete.mockRejectedValue(err);
    const tab = listing();

    await tab.deleteFile(tab.files[2]);

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(tab.files).toHaveLength(3);
    expect(tab.deletingFiles).toEqual([]);
    expect(tab.updateFiles).not.toHaveBeenCalled();
  });

  it('does not delete the same file twice at once', () => {
    axios.delete.mockReturnValue(new Promise(() => {}));
    const tab = listing();
    tab.deleteFile(tab.files[0]);
    tab.deleteFile(tab.files[0]);
    expect(axios.delete).toHaveBeenCalledTimes(1);
  });
});

describe('deleting the selected files', () => {
  it('confirms once with the count, then deletes them together', async () => {
    axios.post.mockResolvedValue({
      data: { deleted: ['a.log', 'c.pcap'], failed: [] },
    });
    const tab = listing();
    tab.selectedFiles = ['a.log', 'c.pcap'];

    tab.confirmDeleteSelectedFiles();
    expect(tab.dialogs).toHaveLength(1);
    expect(tab.dialogs[0].message).toContain('2 selected files');
    expect(tab.dialogs[0].confirmText).toBe('Delete 2 Files');

    const pending = tab.dialogs[0].onConfirm();
    expect(tab.filesBulkDeleting).toBe(true);
    await pending;

    expect(axios.post).toHaveBeenCalledWith('experiments/exp1/files/delete', {
      paths: ['a.log', 'c.pcap'],
    });
    expect(tab.filesBulkDeleting).toBe(false);
    expect(tab.files.map((f) => f.path)).toEqual(['scorch/run-0/b.json']);
    expect(tab.selectedFiles).toEqual([]);
    expect(tab.toasts[0].message).toBe('Deleted 2 files');
    expect(notify.show).not.toHaveBeenCalled();
    expect(tab.updateFiles).toHaveBeenCalled();
  });

  it('asks nothing when nothing is selected', () => {
    const tab = listing();
    tab.confirmDeleteSelectedFiles();
    expect(tab.dialogs).toHaveLength(0);
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
    const tab = listing();
    tab.selectedFiles = ['a.log', 'c.pcap', 'scorch/run-0/b.json'];

    await tab.deleteSelectedFiles();

    expect(notify.show).toHaveBeenCalledWith(
      'Deleted 1 of 3 files; 2 could not be deleted',
      'c.pcap: capture already exists: c.pcap\n' +
        'scorch/run-0/b.json: file not found',
    );
    expect(tab.selectedFiles).toEqual(['c.pcap', 'scorch/run-0/b.json']);
    expect(tab.files).toHaveLength(2);
    expect(tab.toasts).toHaveLength(0);
  });

  it('names the first ten failures and counts the rest', async () => {
    const paths = Array.from({ length: 12 }, (_, i) => `f${i}.log`);
    axios.post.mockResolvedValue({
      data: {
        deleted: [],
        failed: paths.map((path) => ({ path, error: 'busy' })),
      },
    });
    const tab = listing(paths);
    tab.selectedFiles = paths;

    await tab.deleteSelectedFiles();

    const lines = notify.show.mock.calls[0][1].split('\n');
    expect(lines).toHaveLength(11);
    expect(lines[9]).toBe('f9.log: busy');
    expect(lines[10]).toBe('and 2 more');
  });

  it('shows the error and relists when the request fails', async () => {
    const err = new Error('forbidden');
    axios.post.mockRejectedValue(err);
    const tab = listing();
    tab.selectedFiles = ['a.log'];

    await tab.deleteSelectedFiles();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(tab.files).toHaveLength(3);
    expect(tab.filesBulkDeleting).toBe(false);
    expect(tab.updateFiles).toHaveBeenCalled();
  });
});

describe('downloading the selected files', () => {
  it('saves the zip the server streams, spinning until done', async () => {
    const zip = new Blob(['PK']);
    axios.post.mockResolvedValue({ data: zip });
    const tab = listing();
    tab.selectedFiles = ['a.log', 'c.pcap'];

    const pending = tab.downloadSelectedFiles();
    expect(tab.filesDownloading).toBe(true);
    expect(tab.filesBusy).toBe(true);
    await pending;

    expect(axios.post).toHaveBeenCalledWith(
      'experiments/exp1/files/download',
      { paths: ['a.log', 'c.pcap'] },
      { responseType: 'blob' },
    );
    expect(saver.saveAs).toHaveBeenCalledWith(zip, 'exp1-files.zip');
    expect(tab.filesDownloading).toBe(false);
  });

  it('shows the server error text, which arrives as a blob', async () => {
    const err = Object.assign(new Error('Request failed'), {
      response: { status: 413, data: new Blob(['download too large']) },
    });
    axios.post.mockRejectedValue(err);
    const tab = listing();
    tab.selectedFiles = ['a.log'];

    await tab.downloadSelectedFiles();

    expect(notify.error).toHaveBeenCalledWith(err);
    expect(err.response.data).toBe('download too large');
    expect(saver.saveAs).not.toHaveBeenCalled();
    expect(tab.filesDownloading).toBe(false);
  });

  it('waits for a download or delete in progress', async () => {
    const tab = listing();
    tab.selectedFiles = ['a.log'];
    tab.filesBulkDeleting = true;
    await tab.downloadSelectedFiles();
    await tab.deleteSelectedFiles();
    expect(axios.post).not.toHaveBeenCalled();
  });
});

// The tab rendered with the real Buefy components. Buefy's table draws its
// rows only in a browser, so the row tests draw it with renderSSR's stand-in.
describe('rendering', () => {
  // the tab on exp1, with some of its state (data and table fields) set
  function render(state = {}, { table = {}, tables = false } = {}) {
    const tab = {
      ...ExperimentFilesTab,
      setup(props) {
        const bindings = ExperimentFilesTab.setup(props);
        Object.assign(bindings.filesTable, table);
        return bindings;
      },
    };
    return renderSSR(
      tab,
      { paginateKey: 'running-files' },
      { route: '/experiment/exp1', data: state, tables },
    );
  }

  // what each button or link in a cell is for, in order
  const buttons = (cell) =>
    [...cell.matchAll(/<(?:a|button)\b([^>]*)>/g)].map(([, attrs]) => {
      const label = attrs.match(/aria-label="([^"]*)"/)?.[1];
      const href = attrs.match(/href="([^"]*)"/)?.[1];
      if (attrs.includes('aria-hidden="true"')) return '(space)';
      return href ? `${label} -> ${href}` : label;
    });

  it('has no toolbar while nothing is checked and one page holds all', async () => {
    const html = await render();
    expect(html).not.toContain('files-bar');
    expect(html).not.toContain('Paginate');
    expect(html).toContain('class="b-table');
    expect(textOf(html)).toContain('Loading files…');
  });

  it('offers Paginate once the files fill more than a page', async () => {
    const html = await render({}, { table: { total: 11 } });
    expect(html).toContain('class="files-bar"');
    expect(html).toContain('Paginate');
    expect(html).not.toContain('file-selection-actions');
  });

  it("keeps the page's Paginate setting under its own key", async () => {
    const storage = memoryStorage();
    storage.setItem('phenix.paginate.running-files', 'true');
    vi.stubGlobal('localStorage', storage);

    const html = await render({}, { table: { total: 11 } });
    expect(html).toMatch(/<input type="checkbox"[^>]*checked>/);
    expect(html).toContain('class="pagination');
  });

  it('shows the selection toolbar while files are checked', async () => {
    const html = await render({
      files: [row('a.log')],
      selectedFiles: ['a.log'],
    });
    expect(html).toContain('file-selection-actions');
    expect(html).toContain('Download (1)');
    expect(html).toContain('Delete (1)');
  });

  it('gives each row the button that opens it, then download and delete', async () => {
    store.features = ['webshark'];
    const html = await render(
      {
        files: [
          row('caps/a.pcap'),
          row('notes.txt', { plainText: true }),
          row('disk.img'),
        ],
      },
      { tables: true },
    );
    const [capture, text, other] = tableRows(html);

    expect(buttons(capture.actions)).toEqual([
      'Open a.pcap in WebShark -> /packets?exp=exp1&amp;file=caps/a.pcap',
      'Download a.pcap',
      'Delete a.pcap',
    ]);
    expect(capture.actions).toContain('data-icon="shark-fin"');
    expect(buttons(text.actions)).toEqual([
      'View notes.txt',
      'Download notes.txt',
      'Delete notes.txt',
    ]);
    // an unseen button keeps the download buttons in line
    expect(buttons(other.actions)).toEqual([
      '(space)',
      'Download disk.img',
      'Delete disk.img',
    ]);

    // the name opens the file the same way
    expect(textOf(capture.name)).toBe('Open in WebShark a.pcap');
    expect(textOf(text.name)).toBe('view file notes.txt');
    expect(other.name).not.toContain('is-clickable');
    expect(capture.path).toContain('/phenix/images/exp1/files/caps/a.pcap');
  });

  it("leaves out the checkboxes and deletes a role can't use", async () => {
    const files = [row('a.log')];
    rbac.allowed = (_, verb) => verb != 'delete';
    let [cells] = tableRows(await render({ files }, { tables: true }));
    expect(buttons(cells.actions)).toEqual(['(space)', 'Download a.log']);
    expect(cells.multiselect).toBeDefined();

    rbac.allowed = (_, verb) => verb == 'list';
    [cells] = tableRows(await render({ files }, { tables: true }));
    expect(cells.multiselect).toBeUndefined();
  });

  it('spins the toolbar button at work and holds the others', async () => {
    const toolbar = async (props) =>
      (await renderSSR(FileSelectionActions, { count: 2, ...props }))
        .match(/<button[^>]*>/g)
        .map((button) => ({
          loading: button.includes('is-loading'),
          disabled: button.includes('disabled'),
        }));
    const idle = { loading: false, disabled: false };
    const held = { loading: false, disabled: true };
    const can = { canDownload: true, canDelete: true };

    expect(await toolbar(can)).toEqual([idle, idle, idle]);
    expect(await toolbar({ ...can, downloading: true })).toEqual([
      { loading: true, disabled: false },
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
  async function renderPage(view, state) {
    stubBrowser();
    // the page's own lists never arrive
    axios.get.mockReturnValue(new Promise(() => {}));

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
      render: () => h('div'),
    };
    let page;
    await renderSSR(
      {
        ...view,
        components: { ...view.components, ExperimentFilesTab: FilesTab },
        mixins: [
          ...view.mixins,
          {
            created() {
              page = this;
            },
          },
        ],
      },
      {},
      {
        route: '/experiment/exp1',
        data: {
          activeTab: 1,
          experiment: { name: 'exp1', vms: [] },
          fileCategory: 'Packet Capture',
          ...state,
        },
      },
    );
    return { page, tabs };
  }

  it.each(pages)(
    '%s passes the tab its search and category, and takes its news',
    async (_, view, paginateKey, search) => {
      const { page, tabs } = await renderPage(view, search);

      expect(tabs).toEqual([
        { paginateKey, filter: 'web-server', category: 'Packet Capture' },
      ]);
      expect(page.fileCategories).toEqual(['Packet Capture', 'Unknown']);
      expect(page.searchHistory).toEqual(['web-server']);
      expect(page.isWaiting).toBe(true);
      // the tab holds the file list, not the page
      expect(page.files).toBeUndefined();
    },
  );

  it.each(pages)('%s reloads the tab when the category changes', (_, view) => {
    const reload = vi.fn();
    const page = { $refs: { filesTab: { reload } }, fileCategory: null };
    view.methods.assignCategory.call(page, 'Unknown');
    expect(page.fileCategory).toBe('Unknown');
    expect(reload).toHaveBeenCalled();
  });
});
