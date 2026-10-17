// The Tunneler page lists the builds GET /downloads/tunneler returns, and says
// so when there are none or none could be listed.
import { afterEach, describe, expect, it, vi } from 'vitest';

import Tunneler from '@/views/Tunneler.vue';
import { pageContext } from './helpers/page.js';
import { renderSSR, tableRows, textOf } from './helpers/render.js';

afterEach(() => vi.unstubAllGlobals());

// the page once the server answers the list with this response, or fails
async function open(answer) {
  const fetch = vi.fn(async () => {
    if (answer instanceof Error) throw answer;
    return answer;
  });
  vi.stubGlobal('fetch', fetch);

  const ctx = pageContext(Tunneler);
  await Tunneler.created.call(ctx);
  expect(fetch).toHaveBeenCalledWith('/downloads/tunneler');
  return ctx;
}

const json = (body) =>
  new Response(JSON.stringify(body), {
    headers: { 'Content-Type': 'application/json' },
  });

// the page's HTML once its list has been read into ctx
const page = (ctx) =>
  renderSSR(
    Tunneler,
    {},
    {
      route: '/tunneler',
      tables: true,
      data: {
        downloads: ctx.downloads,
        loaded: ctx.loaded,
        failed: ctx.failed,
        notInstalled: ctx.notInstalled,
      },
    },
  );

// what the page shows under its downloads heading: the table's rows, or else
// its message
async function shown(ctx) {
  const html = await page(ctx);
  const below = html.slice(html.indexOf('</h1>'), html.indexOf('<hr'));
  const body = below.includes('<tbody>')
    ? below.slice(below.indexOf('<tbody>'), below.indexOf('</tbody>'))
    : below;
  return {
    text: textOf(body),
    rows: tableRows(below).map((row) => ({
      os: textOf(row.OS),
      arch: textOf(row.Architecture),
      link: row.Download.match(/<a[^>]*>/)[0],
    })),
  };
}

describe('the Tunneler page', () => {
  it('links to each installed build, named by its platform', async () => {
    const ctx = await open(
      json({
        files: [
          'phenix-tunneler-darwin-arm64',
          'phenix-tunneler-linux-amd64',
          'phenix-tunneler-windows-amd64.exe',
          'tunneler custom',
        ],
      }),
    );
    const { rows } = await shown(ctx);

    expect(rows.map(({ os, arch }) => `${os} ${arch}`.trim())).toEqual([
      'MacOS arm64',
      'Linux amd64',
      'Windows amd64',
      'tunneler custom',
    ]);
    expect(rows[1].link).toMatch(
      /href="\/downloads\/tunneler\/phenix-tunneler-linux-amd64"/,
    );
    // saved as a file, not opened
    expect(rows[1].link).toMatch(/\sdownload[\s>]/);
    expect(rows[1].link).toContain(
      'aria-label="Download phenix-tunneler-linux-amd64"',
    );
    expect(rows[3].link).toContain(
      'href="/downloads/tunneler/tunneler%20custom"',
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
    const { text, rows } = await shown(await open(answer));
    expect(text).toBe(message);
    expect(rows).toEqual([]);
  });

  it('puts the downloads before the quick start', async () => {
    const html = await page(await open(json({ files: [] })));
    expect(html.indexOf('id="downloads"')).toBeGreaterThan(-1);
    expect(html.indexOf('id="downloads"')).toBeLessThan(
      html.indexOf('id="quick-start"'),
    );
    expect(textOf(html)).toContain('downloaded via the table above');
  });

  it('says it is loading until the list arrives', async () => {
    vi.stubGlobal('fetch', () => new Promise(() => {}));
    const ctx = pageContext(Tunneler);
    Tunneler.created.call(ctx);
    expect((await shown(ctx)).text).toBe('Loading downloads…');
  });
});
