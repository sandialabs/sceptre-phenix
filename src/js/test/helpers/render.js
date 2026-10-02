// Renders components to HTML on the server with the real Buefy components, so
// a template error or a missing registration fails in a test rather than in
// the browser.
import { createSSRApp, h, inject, provide } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { vi } from 'vitest';

import { installBuefy } from '@/utils/buefy.js';
import { memoryStorage } from './storage.js';

// Every named route in src/router.js, which pages link to by name; keep the
// two lists the same.
const ROUTES = [
  ['/', 'home'],
  ['/signin', 'signin'],
  ['/experiments', 'experiments'],
  ['/experiment/:id', 'experiment'],
  ['/hosts', 'hosts'],
  ['/configs/', 'configs'],
  ['/disks/', 'disks'],
  ['/vmtiles', 'vmtiles'],
  ['/users', 'users'],
  ['/log', 'log'],
  ['/console', 'console'],
  ['/scorch', 'scorch'],
  ['/scorch/:id', 'scorchruns'],
  ['/soh', 'sohoverview'],
  ['/soh/:id', 'soh'],
  ['/settings', 'settings'],
  ['/tunneler', 'tunneler'],
  ['/packets', 'webshark'],
  ['/proxysignup/:username?', 'proxysignup'],
  ['/disabled', 'disabled'],
  ['/builder?token=:token', 'builder'],
  ['/version', 'version'],
  ['/features', 'features'],
  ['/api/v1/options', 'options'],
  ['/api/v1/experiments/:id/files/:name', 'file'],
  ['/api/v1/experiments/:id/vms/:name/vnc?token=:token', 'vnc'],
].map(([path, name]) => ({ path, name, component: {} }));

// names the icon b-icon asks for
const IconName = {
  props: ['icon'],
  render() {
    const name = Array.isArray(this.icon) ? this.icon[1] : this.icon;
    return h('i', { 'data-icon': name });
  },
};

// Buefy's table learns its columns once mounted in a browser, so on the
// server it draws neither headers nor rows. These stand-ins draw a plain
// table instead: a header cell per column, then a row per item with each
// column's cell from its slot. tableRows below reads the rows back.
const ROW = Symbol('table row');

const TableRow = {
  props: ['row'],
  setup(props, { slots }) {
    provide(ROW, props.row);
    return () => h('tr', slots.default?.());
  },
};

const Table = {
  props: { data: Array },
  setup(props, { slots }) {
    const rows = () =>
      (props.data ?? []).map((row) =>
        h(TableRow, { row }, { default: slots.default }),
      );
    return () =>
      h('table', { class: 'table-rows' }, [
        h('thead', h(TableRow, { row: null }, { default: slots.default })),
        h('tbody', props.data?.length ? rows() : slots.empty?.()),
      ]);
  },
};

const TableColumn = {
  props: { field: String, label: String, sortable: Boolean },
  setup(props, { slots }) {
    const row = inject(ROW);
    return () =>
      row === null
        ? h(
            'th',
            {
              'data-field': props.field,
              'data-sortable': props.sortable || undefined,
            },
            slots.header?.({ column: props }) ?? props.label,
          )
        : h(
            'td',
            { 'data-field': props.field, 'data-label': props.label },
            slots.default?.({ row }),
          );
  },
};

// Each body row of a table drawn with the stand-ins: its cells' HTML by
// column, a column named by its field or, when it has none, its label.
export const tableRows = (html) =>
  html
    .slice(html.indexOf('<tbody>'), html.indexOf('</tbody>'))
    .split('<tr>')
    .slice(1)
    .map((tr) =>
      Object.fromEntries(
        [...tr.matchAll(/<td([^>]*)>([\s\S]*?)<\/td>/g)].map(
          ([, attrs, cell]) => [
            textOf(
              (attrs.match(/data-field="([^"]*)"/) ??
                attrs.match(/data-label="([^"]*)"/))[1],
            ),
            cell,
          ],
        ),
      ),
    );

// the Font Awesome layers main.js registers, drawn as their contents
const Layers = {
  render() {
    return h('span', this.$slots.default?.());
  },
};

// The HTML of `component` rendered with `props`. Options:
//   route: where the app's router is, as a path or a location
//   data: state set over the component's data()
//   tables: draw b-table with the stand-ins above
export async function renderSSR(
  component,
  props = {},
  { route = '/', data, tables = false } = {},
) {
  const root = data
    ? {
        ...component,
        data() {
          return { ...component.data?.call(this), ...data };
        },
      }
    : component;
  const router = createRouter({
    history: createMemoryHistory(),
    routes: ROUTES,
  });
  router.push(route);
  await router.isReady();
  const app = createSSRApp(root, props);
  app.use(router);
  app.component('font-awesome-icon', IconName);
  app.component('font-awesome-layers', Layers);
  app.component('font-awesome-layers-text', Layers);
  installBuefy(app);
  if (tables) {
    app.component('b-table', Table);
    app.component('b-table-column', TableColumn);
  }
  return renderToString(app);
}

// the text a reader sees in rendered HTML, with runs of space collapsed
export const textOf = (html) =>
  html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .replaceAll('&#39;', "'")
    .replaceAll('&quot;', '"')
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&amp;', '&');

// whether a tag's attributes make it disabled
const disabledAttr = (attrs) => /(^|\s)disabled(\s|=|$)/.test(attrs);

// Each button in rendered HTML, in order: its text, or its aria-label when
// it has none, then marked ' (submits)' when it submits its form,
// ' (spinning)' while it spins and ' (disabled)' when disabled.
export const buttons = (html) =>
  [...html.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/g)].map(
    ([, attrs, inner]) =>
      (textOf(inner) ||
        textOf(attrs.match(/aria-label="([^"]*)"/)?.[1] ?? '')) +
      (attrs.includes('type="submit"') ? ' (submits)' : '') +
      (/\bis-loading\b/.test(attrs) ? ' (spinning)' : '') +
      (disabledAttr(attrs) ? ' (disabled)' : ''),
  );

// Each option of a rendered select, in order: the label of its optgroup (null
// outside one), its value, text, and whether it is disabled.
export function selectOptions(selectHtml) {
  const found = [];
  let group = null;
  for (const part of selectHtml.split(/(<optgroup[^>]*>|<\/optgroup>)/)) {
    const opened = part.match(/^<optgroup label="([^"]*)"/);
    if (opened) group = opened[1];
    else if (part === '</optgroup>') group = null;
    else {
      for (const [, attrs, text] of part.matchAll(
        /<option\b([^>]*)>([\s\S]*?)<\/option>/g,
      )) {
        found.push({
          group,
          // an empty value renders as a bare attribute
          value: attrs.match(/\svalue(?:="([^"]*)")?/)?.[1] ?? '',
          text: textOf(text),
          disabled: disabledAttr(attrs),
        });
      }
    }
  }
  return found;
}

// the open modal's HTML through its footer, or null when none is open
export const openModal = (html) =>
  html.match(/<div class="modal is-active"[\s\S]*?<\/footer>/)?.[0] ?? null;

// Each label in rendered HTML that names its control with `for`: its text,
// and the tag of the element with that id, or null when there is none.
export const labeledControls = (html) =>
  [
    ...html.matchAll(/<label\b[^>]*\bfor="([^"]*)"[^>]*>([\s\S]*?)<\/label>/g),
  ].map(([, id, text]) => ({
    label: textOf(text),
    control:
      [...html.matchAll(/<(\w+)\b[^>]*\sid="([^"]*)"/g)].find(
        (match) => match[2] === id,
      )?.[1] ?? null,
  }));

// Each button or link a tooltip wraps, in order: the tooltip's text, the
// button's aria-label, whether it is disabled, and its opening tag's
// attributes.
export const tooltipButtons = (html) =>
  [
    ...html.matchAll(
      /class="tooltip-content[^"]*"[^>]*>([\s\S]*?)<\/div><div class="tooltip-trigger"[^>]*>(?:(?!tooltip-content)[\s\S])*?<(?:button|a)\b([^>]*)>/g,
    ),
  ].map(([, tooltip, attrs]) => ({
    tooltip: textOf(tooltip),
    label: textOf(attrs.match(/aria-label="([^"]*)"/)?.[1] ?? ''),
    disabled: disabledAttr(attrs),
    attrs,
  }));

// What pages reach for in a browser while they are created: listeners on the
// window and document, localStorage and the location. Undone by
// vi.unstubAllGlobals().
export function stubBrowser() {
  const target = {
    addEventListener: () => {},
    removeEventListener: () => {},
    querySelector: () => null,
  };
  vi.stubGlobal('window', target);
  vi.stubGlobal('document', target);
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('location', { protocol: 'http:', host: 'phenix.test' });
}
