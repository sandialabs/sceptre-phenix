// Diagram checks as the editor shows them: counted, sorted out by the node or
// connection each one is about, and worded with element names rather than the
// validator's index paths (see issueText).

import { issueText } from './adapters/forms.js';
import { count } from './announce.js';
import { findNode } from './model.js';

/**
 * @param {object[]} issues
 * @returns {{errors: number, warnings: number}}
 */
export function issueCounts(issues = []) {
  const errors = issues.filter((entry) => entry.level === 'error').length;

  return { errors, warnings: issues.length - errors };
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

// "Device web-01", "Switch EXP", "Connection from web-01 to EXP": the name
// issueText gives the element at doc[collection][index].
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
