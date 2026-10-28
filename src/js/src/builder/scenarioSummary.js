// What the Inspector says of a scenario the diagram lists: how many apps it
// has, how many hosts they run on and how many of them are disabled, and
// what to say beside each app.

import { count } from './announce.js';

// The counts of a scenario's apps after its name in the Inspector, as in
// "15 apps, 2 hosts, 1 disabled app". A host counts once.
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

// What the Inspector says in parentheses beside an app's name: "disabled",
// "no hosts", both, or ''.
export function scenarioAppNote(app) {
  return [app?.disabled && 'disabled', !app?.hosts?.length && 'no hosts']
    .filter(Boolean)
    .join(', ');
}
