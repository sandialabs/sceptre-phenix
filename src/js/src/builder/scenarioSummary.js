// What the Inspector says of a scenario the diagram lists: how many apps it
// has, how many hosts they run on and how many of them are disabled, and
// what to say beside each app.

import { count } from './announce.js';

/**
 * The counts of a scenario's apps, as the Inspector shows them after its
 * name. Every app counts, the disabled ones too. A host counts once, also
 * when several apps run on it. The disabled apps are said only when there
 * are some.
 *
 * @param {{name: string, hosts: string[], disabled: boolean}[]} apps the
 *   scenario's apps (see scenarioApps in model.js)
 * @returns {string} for example "1 app, 0 hosts" or "15 apps, 2 hosts,
 *   1 disabled app"
 */
export function scenarioCounts(apps) {
  const list = Array.isArray(apps) ? apps : [];
  const hosts = new Set(list.flatMap((app) => app.hosts || []));
  const disabled = list.filter((app) => app.disabled).length;

  return [
    count(list.length, 'app'),
    count(hosts.size, 'host'),
    disabled > 0 && count(disabled, 'disabled app'),
  ]
    .filter(Boolean)
    .join(', ');
}

/**
 * What the Inspector says beside an app's name, in parentheses: that it is
 * disabled, that it has no hosts, or both.
 *
 * @param {{hosts: string[], disabled: boolean}} app
 * @returns {string} "disabled", "no hosts", "disabled, no hosts", or ''
 */
export function scenarioAppNote(app) {
  return [app?.disabled && 'disabled', !app?.hosts?.length && 'no hosts']
    .filter(Boolean)
    .join(', ');
}
