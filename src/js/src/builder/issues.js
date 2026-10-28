// Diagram checks as the editor shows them: counted, sorted by element and
// worded with element names in place of index paths (see issueText). The check
// lists take every issue, local or from the server, in one shape (see toIssue).

import { issueText } from './adapters/forms.js';
import { count } from './announce.js';
import { findNode } from './model.js';

/**
 * Counts of errors and warnings. `blocking` counts the warnings that publishing
 * refuses, which the Publish dialog lists as errors.
 */
export function issueCounts(issues = []) {
  const errors = issues.filter((entry) => entry.level === 'error').length;
  const blocking = issues.filter(
    (entry) => entry.blocksPublish && entry.level !== 'error',
  ).length;

  return { errors, warnings: issues.length - errors, blocking };
}

/**
 * What stops publishing, as the Publish dialog decides it, or '' when nothing
 * does.
 */
export function publishingText({ errors, warnings, blocking = 0 }) {
  if (!blocking) {
    if (errors) {
      return 'You must fix the errors before you can publish the diagram.';
    }

    return warnings
      ? 'Warnings do not stop you from publishing the diagram.'
      : '';
  }

  let which = `${blocking} of the warnings`;

  if (blocking === warnings) {
    which = warnings === 1 ? 'the warning' : 'the warnings';
  }

  const subject = errors ? `the errors and ${which}` : which;
  const listed =
    blocking === 1 ? 'that warning as an error' : 'those warnings as errors';

  return `You must fix ${subject} before you can publish the diagram. Publish lists ${listed}.`;
}

/**
 * @param {{errors: number, warnings: number}} counts
 * @returns {string} "2 errors, 1 warning", or '' when there are none
 */
export function countsText({ errors, warnings }) {
  return [
    errors > 0 && count(errors, 'error'),
    warnings > 0 && count(warnings, 'warning'),
  ]
    .filter(Boolean)
    .join(', ');
}

/**
 * The node id that an issue is about, or ''. For a network, it is the network's
 * first switch, where the canvas shows the network.
 */
export function issueNodeId(doc, issue) {
  if (issue.nodeId) {
    return issue.nodeId;
  }

  const hub = issue.networkId
    ? (doc?.nodes || []).find(
        (node) =>
          node.kind === 'switch' && node.switch?.networkId === issue.networkId,
      )
    : null;

  return hub?.id || '';
}

// Errors first. Otherwise in the validator's order, which is by path.
function byLevel(entries) {
  return [
    ...entries.filter((entry) => entry.level === 'error'),
    ...entries.filter((entry) => entry.level !== 'error'),
  ];
}

// The issue's text without the element it is about, for a list headed by
// that element's name.
function ownText(doc, issue) {
  return issueText(doc, { ...issue, path: '' });
}

// "Device web-01", "Switch EXP", "Connection from web-01 (eth0) to EXP": the
// name issueText gives the element at doc[collection][index].
function elementTitle(doc, collection, index) {
  return issueText(doc, { path: `${collection}[${index}]`, message: '' })
    .replace(/:\s*$/, '')
    .trim();
}

/**
 * The canvas summary of each node's issues by node id: the worst level and text
 * such as "1 error: a device needs a hostname." See issueNodeId for networks.
 */
export function nodeIssueSummaries(doc, issues = []) {
  const summaries = new Map();

  if (!issues.length) {
    return summaries;
  }

  // Each network's first switch, looked up once rather than per issue.
  const hubs = new Map();

  for (const node of doc?.nodes || []) {
    const networkId = node.kind === 'switch' ? node.switch?.networkId : '';

    if (networkId && !hubs.has(networkId)) {
      hubs.set(networkId, node.id);
    }
  }

  const about = new Map();

  for (const entry of issues) {
    const id = entry.nodeId || hubs.get(entry.networkId) || '';

    if (!id) {
      continue;
    }

    if (!about.has(id)) {
      about.set(id, []);
    }

    about.get(id).push(entry);
  }

  about.forEach((entries, id) => {
    summaries.set(id, issueSummary(doc, entries));
  });

  return summaries;
}

/**
 * The canvas summary of each connection's issues by edge id, as
 * nodeIssueSummaries gives it for nodes.
 */
export function edgeIssueSummaries(doc, issues = []) {
  const about = new Map();

  for (const entry of issues) {
    if (!entry.edgeId) {
      continue;
    }

    if (!about.has(entry.edgeId)) {
      about.set(entry.edgeId, []);
    }

    about.get(entry.edgeId).push(entry);
  }

  return new Map(
    [...about].map(([id, entries]) => [id, issueSummary(doc, entries)]),
  );
}

// The level of an element's mark (error when any issue is one), and its
// issues in words, errors first.
function issueSummary(doc, entries) {
  const counts = issueCounts(entries);
  const part = (level, n) => {
    const texts = entries
      .filter((entry) => (entry.level === 'error') === (level === 'error'))
      .map((entry) => ownText(doc, entry));

    return n ? `${count(n, level)}: ${texts.join('. ')}` : '';
  };

  return {
    level: counts.errors ? 'error' : 'warning',
    text: [part('error', counts.errors), part('warning', counts.warnings)]
      .filter(Boolean)
      .map((sentence) => `${sentence}.`)
      .join(' '),
  };
}

/**
 * The issues, each with `text`, about the Inspector's element, errors first. A
 * switch includes its network's issues. The diagram gets the issues about no
 * node or connection.
 */
export function issuesAbout(doc, issues, selection) {
  if (selection?.type === 'node') {
    const node = findNode(doc, selection.id);
    const networkId = node?.kind === 'switch' ? node.switch?.networkId : '';

    return byLevel(
      issues.filter(
        (entry) =>
          entry.nodeId === selection.id ||
          (!entry.nodeId && networkId && entry.networkId === networkId),
      ),
    ).map((entry) => ({ ...entry, text: ownText(doc, entry) }));
  }

  if (selection?.type === 'edge') {
    return byLevel(issues.filter((entry) => entry.edgeId === selection.id)).map(
      (entry) => ({ ...entry, text: ownText(doc, entry) }),
    );
  }

  return byLevel(
    issues.filter((entry) => !entry.edgeId && !issueNodeId(doc, entry)),
  ).map((entry) => ({ ...entry, text: issueText(doc, entry) }));
}

// --- one shape for every list ------------------------------------------------

const SEVERITIES = ['error', 'warning'];

// The keys an issue keeps, when they hold text.
const ISSUE_KEYS = ['code', 'path', 'nodeId', 'edgeId', 'networkId', 'field'];

// The element a document path starts at: "nodes[3]", "networks[0]".
const PATH_START = /^(nodes|edges|networks)\[(\d+)\]/;
const ID_KEYS = { nodes: 'nodeId', edges: 'edgeId', networks: 'networkId' };

/**
 * A check in the one shape that the check lists take: from a message alone, a
 * validateDocument() issue or a server issue. With `publishing`, a warning that
 * publishing refuses becomes an error, as the Publish dialog lists it.
 */
export function toIssue(
  entry,
  defaultSeverity = 'error',
  { publishing = false } = {},
) {
  const fallback = SEVERITIES.includes(defaultSeverity)
    ? defaultSeverity
    : 'error';

  if (!entry || typeof entry !== 'object') {
    return { severity: fallback, message: entry ? String(entry) : '' };
  }

  const stated = [entry.severity, entry.level].find((value) =>
    SEVERITIES.includes(value),
  );
  const issue = {
    severity: publishing && entry.blocksPublish ? 'error' : stated || fallback,
    message: typeof entry.message === 'string' ? entry.message : '',
  };

  ISSUE_KEYS.forEach((key) => {
    if (typeof entry[key] === 'string' && entry[key].trim()) {
      issue[key] = entry[key];
    }
  });

  if (entry.blocksPublish) {
    issue.blocksPublish = true;
  }

  return issue;
}

// An issue that says nothing of where it is or what it is: a message alone.
function bare(issue) {
  return ISSUE_KEYS.every((key) => !issue[key]);
}

// An issue as the server words it in a message alone: its path, then its
// message (see Issue.String in types/builder/validate.go).
function issueWords(issue) {
  return issue.path ? `${issue.path}: ${issue.message}` : issue.message;
}

// Whether two issues of a server answer are one: the same in every key, or,
// where one is a message alone, the same words.
function sameIssue(a, b) {
  if (a.severity !== b.severity) {
    return false;
  }

  if (!bare(a) && !bare(b)) {
    return (
      a.message === b.message && ISSUE_KEYS.every((key) => a[key] === b[key])
    );
  }

  return a.message === b.message || issueWords(a) === issueWords(b);
}

/**
 * The issues of a server response in the one shape: `errors`, `warnings`, then
 * `issues`. An issue listed twice, also as a message alone, is kept once where
 * it came first, as the issue object.
 */
export function responseIssues(data) {
  const list = (value) => (Array.isArray(value) ? value : []);
  const issues = [];

  [
    ...list(data?.errors).map((entry) => toIssue(entry, 'error')),
    ...list(data?.warnings).map((entry) => toIssue(entry, 'warning')),
    ...list(data?.issues).map((entry) => toIssue(entry, 'error')),
  ].forEach((issue) => {
    const index = issues.findIndex((listed) => sameIssue(listed, issue));

    if (index < 0) {
      issues.push(issue);
    } else if (bare(issues[index]) && !bare(issue)) {
      issues[index] = issue;
    }
  });

  return issues;
}

// A path in the validator's form: "nodes[3].device.hostname". A JSON
// pointer ("/nodes/3/device/hostname") and dotted indexes ("nodes.3.device")
// are read as it.
function bracketPath(path) {
  const text = typeof path === 'string' ? path.trim() : '';
  const dotted = text.startsWith('/')
    ? text.slice(1).split('/').join('.')
    : text;

  return dotted.replace(/\.(\d+)(?=\.|\[|$)/g, '[$1]');
}

// A path in JSON Forms' form, the Inspector's data paths:
// "spec.network.interfaces.0.vlan".
function dataPath(path) {
  return String(path || '')
    .replace(/\[(\d+)\]/g, '.$1')
    .replace(/^\.+/, '');
}

// The Inspector field that holds a document path: the part of the path below
// the element, which a node holds under the key of its kind. For example,
// "nodes[2].device.hostname" is a device's "hostname", and "networks[0].name"
// is its switch's "name". '' when the path names no field below the element.
function pathField(doc, path) {
  const match = /^(nodes|edges|networks)\[(\d+)\]\.(.+)$/.exec(
    bracketPath(path),
  );

  if (!match) {
    return '';
  }

  const [, collection, index, rest] = match;

  if (collection !== 'nodes') {
    return dataPath(rest);
  }

  const kind = doc?.nodes?.[Number(index)]?.kind;

  return kind && rest.startsWith(`${kind}.`)
    ? dataPath(rest.slice(kind.length + 1))
    : '';
}

/**
 * The Inspector field path that an issue is about, such as
 * "spec.network.interfaces.0.vlan": its `field`, or its path below the element.
 * '' when it names no field.
 */
export function issueField(doc, issue) {
  const field = typeof issue?.field === 'string' ? issue.field.trim() : '';

  if (field) {
    return PATH_START.test(bracketPath(field))
      ? pathField(doc, field)
      : dataPath(bracketPath(field));
  }

  return pathField(doc, issue?.path);
}

// The ids of what an issue is about: those it names, else that of the
// element its path starts at, as validateDocument() finds it.
function issueIds(doc, issue) {
  if (issue?.nodeId || issue?.edgeId || issue?.networkId) {
    return issue;
  }

  const match = PATH_START.exec(bracketPath(issue?.path));
  const id = match ? doc?.[match[1]]?.[Number(match[2])]?.id : '';

  return typeof id === 'string' && id.trim() ? { [ID_KEYS[match[1]]]: id } : {};
}

// The element an issue is about, as one key: its node, connection or
// network (see issueIds), or none for an issue about the whole diagram.
function elementKey(doc, issue) {
  const { nodeId = '', edgeId = '', networkId = '' } = issueIds(doc, issue);

  return JSON.stringify([nodeId, edgeId, networkId]);
}

/**
 * The dry run issues that the Publish dialog's checks do not already list. The
 * match is on code and element only, because the server uses its own words and
 * severity. Issues with no code, or only the server finds, stay.
 */
export function issuesNotInChecks(doc, issues = [], checks = []) {
  const key = (issue) => JSON.stringify([issue.code, elementKey(doc, issue)]);
  const checked = new Set(
    checks.filter((check) => check?.code).map((check) => key(check)),
  );

  return issues.filter((issue) => !issue?.code || !checked.has(key(issue)));
}

/**
 * Where Go to takes an issue: its node or connection, or a network's first
 * switch (see issueNodeId), with the field it names. Null when the diagram has
 * nothing the issue names.
 */
export function issueTarget(doc, issue) {
  const { nodeId, edgeId, networkId } = issueIds(doc, issue);
  const field = issueField(doc, issue);

  if (nodeId && findNode(doc, nodeId)) {
    return { kind: 'nodes', id: nodeId, field };
  }

  if (edgeId && (doc?.edges || []).some((edge) => edge.id === edgeId)) {
    return { kind: 'edges', id: edgeId, field };
  }

  const hub = networkId ? issueNodeId(doc, { networkId }) : '';

  return hub ? { kind: 'nodes', id: hub, field } : null;
}

// The name of what an issue is about, as its list shows it: the element at
// the start of its path ("Network EXP"), or else the element that Go to
// takes it to.
function issueElement(doc, issue, target) {
  const match = PATH_START.exec(bracketPath(issue.path));

  if (match && doc?.[match[1]]?.[Number(match[2])]) {
    return elementTitle(doc, match[1], Number(match[2]));
  }

  const index = target
    ? (doc?.[target.kind] || []).findIndex((entry) => entry.id === target.id)
    : -1;

  return index >= 0 ? elementTitle(doc, target.kind, index) : '';
}

// The position of an issue in the lists: by its node, in diagram order, then
// by its connection, then the issues about neither.
function diagramRank(doc, entry) {
  const target = entry.target ?? issueTarget(doc, entry);
  const index = target
    ? (doc?.[target.kind] || []).findIndex((item) => item.id === target.id)
    : -1;

  if (index < 0) {
    return [2, 0];
  }

  return [target.kind === 'nodes' ? 0 : 1, index];
}

/**
 * The issues as the check lists show them (see toIssue), each with `text`,
 * `element` (such as "Device web-01") and `target` (see issueTarget).
 */
export function issueEntries(doc, issues = [], { publishing = false } = {}) {
  return issues.map((entry) => {
    const issue = toIssue(entry, 'error', { publishing });
    const target = issueTarget(doc, issue);

    return {
      ...issue,
      text: ownText(doc, issue),
      element: issueElement(doc, issue, target),
      target,
    };
  });
}

/**
 * The issues in two groups, errors then warnings. With the diagram, each group
 * lists node issues in diagram order, then connection issues, then the rest.
 */
export function bySeverity(issues = [], doc = null) {
  const severity = (entry) =>
    (entry.severity || entry.level) === 'error' ? 'error' : 'warning';
  let ordered = issues;

  if (doc) {
    const ranks = new Map(
      issues.map((entry) => [entry, diagramRank(doc, entry)]),
    );

    ordered = [...issues].sort((a, b) => {
      const [first, second] = [ranks.get(a), ranks.get(b)];

      return first[0] - second[0] || first[1] - second[1];
    });
  }

  return [
    {
      severity: 'error',
      label: 'Errors',
      issues: ordered.filter((entry) => severity(entry) === 'error'),
    },
    {
      severity: 'warning',
      label: 'Warnings',
      issues: ordered.filter((entry) => severity(entry) === 'warning'),
    },
  ];
}

/**
 * A group heading: "2 errors block publishing" for blocking issues, else "2
 * errors" or "1 warning".
 */
export function severityHeading(
  { severity, issues },
  { blocking = false } = {},
) {
  const n = issues.length;

  if (severity !== 'error') {
    return count(n, 'warning');
  }

  return blocking
    ? `${count(n, 'error')} ${n === 1 ? 'blocks' : 'block'} publishing`
    : count(n, 'error');
}

/**
 * The accessible name of a Go to button: "Go to device web-01: a device needs
 * a hostname".
 */
export function goToName({ element, text }) {
  const where = element ? `${element[0].toLowerCase()}${element.slice(1)}` : '';

  return [`Go to${where ? ` ${where}` : ''}`, text].filter(Boolean).join(': ');
}
