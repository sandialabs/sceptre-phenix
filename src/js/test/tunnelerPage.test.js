// The Tunneler page lists the builds GET /downloads/tunneler returns, and says
// so when there are none or none could be listed.
import { afterEach, describe, expect, it, vi } from 'vitest';

import Tunneler from '@/views/Tunneler.vue';
import { never } from './helpers/async.js';
import { mountPage, tableRows, textOf } from './helpers/mount.js';

afterEach(() => vi.unstubAllGlobals());

// the page once the server answers the list with this response, or fails
async function open(answer) {
  const fetch = vi.fn(async () => {
    if (answer instanceof Error) throw answer;
    return answer;
  });
  vi.stubGlobal('fetch', fetch);

  const page = await mountPage(Tunneler, { route: '/tunneler' });
  expect(fetch).toHaveBeenCalledWith('/downloads/tunneler');
  return page;
}

const json = (body) =>
  new Response(JSON.stringify(body), {
    headers: { 'Content-Type': 'application/json' },
  });

// what the page shows under its downloads heading: the table's body, or else
// its message, and the table's rows
function shown(page) {
  const body = page.find('#downloads tbody');
  return {
    text: textOf(body.exists() ? body : page.find('#downloads p')),
    rows: tableRows(page).map((row) => ({
      os: textOf(row.OS),
      arch: textOf(row.Architecture),
      link: row.Download.find('a'),
    })),
  };
}

describe('the Tunneler page', () => {
  it('links to each installed build, named by its platform', async () => {
    const page = await open(
      json({
        files: [
          'phenix-tunneler-darwin-arm64',
          'phenix-tunneler-linux-amd64',
          'phenix-tunneler-windows-amd64.exe',
          'tunneler custom',
        ],
      }),
    );
    const { rows } = shown(page);

    expect(rows.map(({ os, arch }) => `${os} ${arch}`.trim())).toEqual([
      'MacOS arm64',
      'Linux amd64',
      'Windows amd64',
      'tunneler custom',
    ]);
    const linux = rows[1].link;
    expect(linux.attributes('href')).toBe(
      '/downloads/tunneler/phenix-tunneler-linux-amd64',
    );
    // saved as a file, not opened
    expect(linux.attributes('download')).toBeDefined();
    expect(linux.attributes('aria-label')).toBe(
      'Download phenix-tunneler-linux-amd64',
    );
    expect(rows[3].link.attributes('href')).toBe(
      '/downloads/tunneler/tunneler%20custom',
    );
  });

  it.each([
    [
      'says no builds are installed when the list is empty',
      json({ files: [] }),
      'No tunneler builds are installed on this server',
    ],
    [
      'says downloads are not installed when the server answers with its own page',
      new Response('<!doctype html>', {
        headers: { 'Content-Type': 'text/html' },
      }),
      'Tunneler downloads are not installed on this phēnix server.',
    ],
    [
      'says downloads are not installed when the server has no such route',
      new Response('404 page not found', { status: 404 }),
      'Tunneler downloads are not installed on this phēnix server.',
    ],
    [
      'says the list failed when the server fails',
      new Response('unable to list tunneler downloads', { status: 500 }),
      'Could not list the tunneler downloads',
    ],
    [
      'says the list failed when the request fails',
      new TypeError('Failed to fetch'),
      'Could not list the tunneler downloads',
    ],
  ])('%s', async (_, answer, message) => {
    const { text, rows } = shown(await open(answer));
    expect(text).toBe(message);
    expect(rows).toEqual([]);
  });

  it('puts the downloads before the quick start', async () => {
    const page = await open(json({ files: [] }));
    const downloads = page.find('#downloads').element;
    expect(
      downloads.compareDocumentPosition(page.find('#quick-start').element) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(textOf(page)).toContain('downloaded via the table above');
  });

  it('says it is loading until the list arrives', async () => {
    vi.stubGlobal('fetch', never);
    const page = await mountPage(Tunneler, { route: '/tunneler' });
    expect(shown(page).text).toBe('Loading downloads…');
  });
});
