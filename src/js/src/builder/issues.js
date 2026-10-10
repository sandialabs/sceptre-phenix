// Diagram checks as the editor shows them: counted, sorted out by the node or
// connection each one is about, and worded with element names rather than the
// validator's index paths (see issueText). The lists of the Publish and
// Checks dialogs take every issue in one shape (see toIssue), whether the
// editor's own checks or the server reported it, group them by severity
// (see bySeverity) and go to the element each is about (see issueTarget).

import { issueText } from './adapters/forms.js';
import { count } from './announce.js';
import { findNode } from './model.js';

/**
 * @param {object[]} issues
 * @returns {{errors: number, warnings: number, blocking: number}} blocking:
 *   the warnings about what publishing refuses, an interface with no VLAN,
 *   an address two interfaces use and a hostname phenix refuses, which the
 *   Publish dialog lists as errors (see publishChecks)
 */
export function issueCounts(issues = []) {
  const errors = issues.filter((entry) => entry.level === 'error').length;
  const blocking = issues.filter(
    (entry) => entry.blocksPublish && entry.level !== 'error',
  ).length;

  return { errors, warnings: issues.length - errors, blocking };
}

/**
 * What stops the diagram from being published, as the Publish dialog
 * decides it: its errors, and the warnings it lists as errors.
 *
 * @param {{errors: number, warnings: number, blocking?: number}} counts
 * @returns {string} a sentence, or '' when there are no issues
 */
export function publishingText({ errors, warnings, blocking = 0 }) {
  if (!blocking) {
    if (errors) {
      return 'Errors must be fixed before the diagram can be published.';
    }

    return warnings
      ? 'Warnings do not stop the diagram from being published.'
      : '';
  }

  let which = `${blocking} of the warnings`;

  if (blocking === warnings) {
    which = warnings === 1 ? 'the warning' : 'the warnings';
  }

  const subject = errors ? `Errors and ${which}` : which;
  const listed =
    blocking === 1 ? 'that warning as an error' : 'those warnings as errors';

  return `${subject[0].toUpperCase()}${subject.slice(1)} must be fixed before the diagram can be published, and Publish lists ${listed}.`;
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
 * The node an issue is about: its own, or for an issue about a network, the
 * network's first switch, which is where the canvas shows the network.
 *
 * @param {object} doc
 * @param {object} issue
 * @returns {string} node id, or '' when it is about no node
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

// Errors first; otherwise in the validator's order, which is by path.
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
 * What the diagram checks say about each node, for the canvas: whether any
 * is an error, and the node's description of them, as "1 error: hostname
 * is required." An issue about a network is about its first switch, as in
 * issueNodeId.
 *
 * @param {object} doc
 * @param {object[]} issues
 * @returns {Map<string, {level: 'error'|'warning', text: string}>} by node
 *   id, for the nodes with any
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
    const counts = issueCounts(entries);
    const part = (level, n) => {
      const texts = entries
        .filter((entry) => (entry.level === 'error') === (level === 'error'))
        .map((entry) => ownText(doc, entry));

      return n ? `${count(n, level)}: ${texts.join('; ')}` : '';
    };

    summaries.set(id, {
      level: counts.errors ? 'error' : 'warning',
      text: [part('error', counts.errors), part('warning', counts.warnings)]
        .filter(Boolean)
        .map((sentence) => `${sentence}.`)
        .join(' '),
    });
  });

  return summaries;
}

/**
 * The issues about what the Inspector shows, each with its text: a node's
 * (a switch's include its network's), a connection's, or for the diagram,
 * those about no node or connection. Errors come first.
 *
 * @param {object} doc
 * @param {object[]} issues
 * @param {{type: string, id?: string}} selection see inspectorSelection
 * @returns {object[]} issues, each with `text`
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

/**
 * The checks dialog's lists: the issues about each node and each connection,
 * in diagram order, and the issues about neither. Each issue has its text;
 * in a node's or connection's list that leaves out the name its list is
 * headed by. Errors come first in every list.
 *
 * @param {object} doc
 * @param {object[]} issues
 * @returns {{nodes: {id: string, title: string, issues: object[]}[],
 *   edges: {id: string, title: string, issues: object[]}[],
 *   diagram: object[]}}
 */
export function issueGroups(doc, issues) {
  const about = { nodes: new Map(), edges: new Map() };
  const diagram = [];

  issues.forEach((entry) => {
    const nodeId = issueNodeId(doc, entry);
    const [collection, id] = nodeId
      ? ['nodes', nodeId]
      : ['edges', entry.edgeId || ''];

    if (!id) {
      diagram.push({ ...entry, text: issueText(doc, entry) });

      return;
    }

    about[collection].set(id, [
      ...(about[collection].get(id) || []),
      { ...entry, text: ownText(doc, entry) },
    ]);
  });

  const groups = (collection) => {
    const seen = new Set();

    return (doc?.[collection] || []).flatMap((element, index) => {
      const entries = about[collection].get(element.id);

      if (!entries || seen.has(element.id)) {
        return [];
      }

      seen.add(element.id);

      return [
        {
          id: element.id,
          title: elementTitle(doc, collection, index),
          issues: byLevel(entries),
        },
      ];
    });
  };

  return {
    nodes: groups('nodes'),
    edges: groups('edges'),
    diagram: byLevel(diagram),
  };
}

// --- one shape for every list ------------------------------------------------

const SEVERITIES = ['error', 'warning'];

// The keys an issue keeps, when they hold text.
const ISSUE_KEYS = ['code', 'path', 'nodeId', 'edgeId', 'networkId', 'field'];

// The element a document path starts at: "nodes[3]", "networks[0]".
const PATH_START = /^(nodes|edges|networks)\[(\d+)\]/;
const ID_KEYS = { nodes: 'nodeId', edges: 'edgeId', networks: 'networkId' };

/**
 * A check in the one shape the lists of checks take, whoever reported it:
 * a message alone, an issue of validateDocument() (`level`, and
 * `blocksPublish` for a warning about what publishing refuses), or an issue
 * the server reports (`severity`, and a `code`).
 *
 * @param {string|object} entry
 * @param {'error'|'warning'} [defaultSeverity] the severity of a message
 *   alone, or of an issue that states none
 * @param {{publishing?: boolean}} [options] publishing: a warning about what
 *   publishing refuses is an error, as the Publish dialog lists it (see
 *   publishChecks)
 * @returns {{severity: 'error'|'warning', message: string, code?: string,
 *   path?: string, nodeId?: string, edgeId?: string, networkId?: string,
 *   field?: string, blocksPublish?: true}}
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
 * The issues a server answer lists, in the one shape: its `errors`, then
 * its `warnings`, each a message or an issue object, then its `issues`,
 * issue objects each of the severity it states (an error when it states
 * none). An issue listed twice, as the same object or as a message alone
 * that says the same as an issue object, is listed once, where it came
 * first, as the issue object.
 *
 * @param {object} [data] a publish result, or the body of a refusal
 * @returns {object[]} see toIssue
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

// The Inspector field a document path is in: the part of the path below
// the element, which a node holds under its kind's key ("nodes[2].device.
// hostname" is a device's "hostname"; "networks[0].name" is its switch's
// "name"). '' when the path names no field below the element.
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
 * The Inspector field an issue is about, as a data path of the form the
 * Inspector shows its element in ("hostname", "spec.network.interfaces.0.
 * vlan"): the issue's own `field`, else the part of its path below the
 * element.
 *
 * @param {object} doc
 * @param {object} issue
 * @returns {string} '' when it names no field
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

/**
 * Where Go to takes an issue: the node or connection it is about, or for
 * an issue about a network, the network's first switch, where the canvas
 * shows the network (see issueNodeId); with the Inspector field it names.
 *
 * @param {object} doc
 * @param {object} issue
 * @returns {{kind: 'nodes'|'edges', id: string, field: string}|null} null
 *   when the diagram has nothing the issue names
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

// The name of what an issue is about, as its list shows it: the element
// its path starts at ("Network EXP"), else the one Go to takes it to.
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

// Where an issue comes in the lists: by its node, in diagram order, then
// by its connection, then the issues about neither, as issueGroups orders
// them.
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
 * The issues as the lists of checks show them, in the one shape (see
 * toIssue), each with its text, the name of the element it is about and
 * where Go to takes it.
 *
 * @param {object} doc
 * @param {Array<string|object>} issues
 * @param {{publishing?: boolean, defaultSeverity?: 'error'|'warning'}}
 *   [options] see toIssue
 * @returns {object[]} each issue with `text` (its message, with the
 *   elements it names by index named), `element` ("Device web-01", or '')
 *   and `target` (see issueTarget)
 */
export function issueEntries(
  doc,
  issues = [],
  { publishing = false, defaultSeverity = 'error' } = {},
) {
  return issues.map((entry) => {
    const issue = toIssue(entry, defaultSeverity, { publishing });
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
 * The issues by severity: the errors, then the warnings. With the diagram,
 * each list is in the order the checks dialog listed them by element: the
 * nodes' issues in diagram order, then the connections', then those about
 * neither; otherwise in the order given.
 *
 * @param {object[]} issues see toIssue (an issue with `level` alone counts
 *   by it)
 * @param {object} [doc]
 * @returns {{severity: 'error'|'warning', label: string, issues:
 *   object[]}[]} both groups, either of them empty
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
 * The heading of a group of issues: "2 errors block publishing" where they
 * stop the diagram from being published, else "2 errors"; "1 warning".
 *
 * @param {{severity: string, issues: object[]}} group see bySeverity
 * @param {{blocking?: boolean}} [options]
 * @returns {string}
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
 * The accessible name of an issue's Go to button: "Go to device web-01:
 * hostname is required".
 *
 * @param {{element: string, text: string}} entry see issueEntries
 * @returns {string}
 */
export function goToName({ element, text }) {
  const where = element ? `${element[0].toLowerCase()}${element.slice(1)}` : '';

  return [`Go to${where ? ` ${where}` : ''}`, text].filter(Boolean).join(': ');
}
