// @vitest-environment happy-dom
import { describe, expect, test } from 'vitest';

import router from '@/router.js';
import { pageTitle, routeTitle } from '@/utils/pageTitle.js';

describe('pageTitle', () => {
  test('puts the most specific part first, then the app', () => {
    expect(pageTitle('demo', 'Experiment')).toBe('demo - Experiment - phēnix');
    expect(pageTitle(undefined, 'Hosts')).toBe('Hosts - phēnix');
    expect(pageTitle()).toBe('phēnix');
  });
});

describe('routeTitle', () => {
  test.each([
    ['/experiments', 'Experiments - phēnix'],
    ['/experiment/demo', 'demo - Experiment - phēnix'],
    ['/soh/demo', 'demo - State of health - phēnix'],
    ['/scorch', 'SCORCH - phēnix'],
    ['/scorch/demo', 'demo - SCORCH runs - phēnix'],
    ['/configs/', 'Configs - phēnix'],
    ['/disks/', 'Disks - phēnix'],
    ['/glossary', 'Glossary - phēnix'],
    // The user name in the path is not the item of the page.
    ['/proxysignup/alice', 'Sign up - phēnix'],
  ])('%s is titled %s', (path, title) => {
    expect(routeTitle(router.resolve(path))).toBe(title);
  });

  test('a route without a title is titled with the app', () => {
    expect(routeTitle({ meta: {}, params: {} })).toBe('phēnix');
  });
});

test('the glossary is a route for every user, with no RBAC check', () => {
  const route = router.resolve('/glossary');
  expect(route.name).toBe('glossary');
  expect(route.meta).toEqual({ title: 'Glossary' });
});

test('back and forward restore the scroll position; a hash goes to its term', () => {
  const scroll = router.options.scrollBehavior;
  const saved = { left: 0, top: 640 };

  expect(scroll({ hash: '#vlan' }, {}, saved)).toBe(saved);
  expect(scroll({ hash: '#vlan' }, {}, null)).toEqual({ el: '#vlan' });
  expect(scroll({ hash: '' }, {}, null)).toBeUndefined();
});
