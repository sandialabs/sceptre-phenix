// The experiment page's tabs: named in its URL with their search, so a reload
// or a shared link opens the same place, and each keeping its own search.
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';

import {
  TAB,
  sameQuery,
  searchFromQuery,
  searchKey,
  tabFromQuery,
  tabQuery,
  vmDetailsRoute,
  withoutVm,
} from '@/components/experiment/experimentTabs.js';

describe('the URL', () => {
  const offered = [TAB.VMS, TAB.FILES, TAB.VNC];

  it.each([
    [{ tab: 'files' }, TAB.FILES],
    [{ tab: 'vnc' }, TAB.VNC],
    // a tab the page does not offer this role, or none at all
    [{ tab: 'netflow' }, TAB.VMS],
    [{ tab: '1' }, TAB.VMS],
    [{ tab: ['files', 'vnc'] }, TAB.VMS],
    [{}, TAB.VMS],
    [undefined, TAB.VMS],
  ])('with the query %j opens the %s tab', (query, tab) => {
    expect(tabFromQuery(query, offered)).toBe(tab);
  });

  it.each([
    [{ q: 'web' }, 'web'],
    [{ q: ['a', 'b'] }, ''],
    [{}, ''],
  ])('with the query %j searches for "%s"', (query, search) => {
    expect(searchFromQuery(query)).toBe(search);
  });

  it('names the tab and search, leaving out the defaults and keeping the rest', () => {
    expect(tabQuery({ tab: 'files', q: 'x', other: '1' }, TAB.VMS, '')).toEqual(
      { other: '1' },
    );
    expect(tabQuery({}, TAB.NETFLOW, 'udp')).toEqual({
      tab: 'netflow',
      q: 'udp',
    });
  });

  it('reads back the tab and search it names', () => {
    fc.assert(
      fc.property(
        fc.constantFrom(...Object.values(TAB)),
        fc.string(),
        fc.dictionary(
          fc.string().filter((k) => !['tab', 'q'].includes(k)),
          fc.string(),
        ),
        (tab, search, rest) => {
          const query = tabQuery(rest, tab, search);
          expect(tabFromQuery(query, Object.values(TAB))).toBe(tab);
          expect(searchFromQuery(query)).toBe(search);
          expect(sameQuery(tabQuery(query, TAB.VMS, ''), rest)).toBe(true);
        },
      ),
    );
  });

  it('gives the VNC tab the VMs tab search', () => {
    expect(Object.values(TAB).map(searchKey)).toEqual([
      TAB.VMS,
      TAB.FILES,
      TAB.VMS,
      TAB.NETFLOW,
    ]);
  });
});

describe('a link to a VM', () => {
  it("names the experiment page with the VM's details open", () => {
    expect(vmDetailsRoute('exp1', 'web')).toEqual({
      name: 'experiment',
      params: { id: 'exp1' },
      query: { vm: 'web' },
    });
  });

  it('leaves the rest of a query when the VM goes', () => {
    expect(withoutVm({ vm: 'web', tab: 'files', q: 'log' })).toEqual({
      tab: 'files',
      q: 'log',
    });
    expect(withoutVm({})).toEqual({});
  });
});
