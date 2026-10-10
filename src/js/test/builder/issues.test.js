import { describe, expect, test } from 'vitest';

import {
  bySeverity,
  countsText,
  goToName,
  issueCounts,
  issueEntries,
  issueField,
  issueGroups,
  issueNodeId,
  issueTarget,
  issuesAbout,
  nodeIssueSummaries,
  publishingText,
  responseIssues,
  severityHeading,
  toIssue,
} from '@/builder/issues.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

// The sample with a duplicate hostname on bravo (an error), a VLAN that
// names no network on alpha, a connection to an unknown network, a network
// with no switch and one that conflicts with EXP.
function brokenSample() {
  const sample = sampleDocument();
  const { doc, alpha, bravo, edge } = sample;
  const broken = {
    ...doc,
    networks: [
      ...doc.networks,
      { id: 'n-lone', name: 'LONE' },
      { id: 'n-dup', name: 'exp' },
    ],
    nodes: doc.nodes.map((node) => {
      if (node.id === bravo.id) {
        const copy = JSON.parse(JSON.stringify(node));

        copy.device.hostname = 'alpha';
        copy.device.spec.general.hostname = 'alpha';

        return copy;
      }

      if (node.id === alpha.id) {
        const copy = JSON.parse(JSON.stringify(node));

        copy.device.spec.network.interfaces.push({
          name: 'eth9',
          vlan: 'GHOST',
        });

        return copy;
      }

      return node;
    }),
    edges: [{ ...edge, networkId: 'n-gone' }],
  };

  return { ...sample, doc: broken, issues: validateDocument(broken) };
}

describe('diagram checks', () => {
  test('are counted by level and said in words', () => {
    const { issues } = brokenSample();
    const counts = issueCounts(issues);

    expect(counts.errors + counts.warnings).toBe(issues.length);
    expect(countsText({ errors: 1, warnings: 0 })).toBe('1 error');
    expect(countsText({ errors: 2, warnings: 1 })).toBe('2 errors, 1 warning');
    expect(countsText({ errors: 0, warnings: 0 })).toBe('');
  });

  // Publish lists a warning about an interface with no VLAN as an error.
  test('say what stops publishing, as Publish decides it', () => {
    const { issues } = brokenSample();

    expect(issueCounts(issues).blocking).toBe(
      issues.filter((issue) => issue.blocksPublish).length,
    );
    expect(issueCounts(issues).blocking).toBeGreaterThan(0);
    expect(publishingText({ errors: 0, warnings: 0 })).toBe('');
    expect(publishingText({ errors: 0, warnings: 2 })).toBe(
      'Warnings do not stop the diagram from being published.',
    );
    expect(publishingText({ errors: 1, warnings: 2 })).toBe(
      'Errors must be fixed before the diagram can be published.',
    );
    expect(publishingText({ errors: 0, warnings: 1, blocking: 1 })).toBe(
      'The warning must be fixed before the diagram can be published, and Publish lists that warning as an error.',
    );
    expect(publishingText({ errors: 0, warnings: 3, blocking: 1 })).toBe(
      '1 of the warnings must be fixed before the diagram can be published, and Publish lists that warning as an error.',
    );
    expect(publishingText({ errors: 2, warnings: 2, blocking: 2 })).toBe(
      'Errors and the warnings must be fixed before the diagram can be published, and Publish lists those warnings as errors.',
    );
  });

  // Each interface that uses the address gets the warning, so both nodes
  // are marked and listed, and both stop publishing.
  test('an address two devices use marks both, and stops publishing', () => {
    const sample = sampleDocument();
    const { alpha, bravo } = sample;
    const doc = {
      ...sample.doc,
      nodes: sample.doc.nodes.map((node) => {
        if (node.kind !== 'device') {
          return node;
        }

        const copy = JSON.parse(JSON.stringify(node));

        Object.assign(copy.device.spec.network.interfaces[0], {
          vlan: 'EXP',
          proto: 'static',
          address: '10.0.0.5',
        });

        return copy;
      }),
    };
    const issues = validateDocument(doc);
    const of = (hostname, other) =>
      `IP address 10.0.0.5 of interface "eth0" of "${hostname}" is also used on VLAN "EXP" by interface "eth0" of "${other}"`;

    expect(nodeIssueSummaries(doc, issues).get(alpha.id)).toEqual({
      level: 'warning',
      text: `1 warning: ${of('alpha', 'bravo')}.`,
    });
    expect(nodeIssueSummaries(doc, issues).get(bravo.id).text).toContain(
      of('bravo', 'alpha'),
    );
    expect(
      issueGroups(doc, issues).nodes.map((group) => [
        group.title,
        group.issues
          .filter((entry) => entry.blocksPublish)
          .map((entry) => entry.text),
      ]),
    ).toEqual([
      ['Device alpha', [of('alpha', 'bravo')]],
      ['Device bravo', [of('bravo', 'alpha')]],
    ]);
    // Bravo's unconnected eth0 is the third warning.
    expect(issueCounts(issues)).toEqual({
      errors: 0,
      warnings: 3,
      blocking: 2,
    });
    expect(publishingText(issueCounts(issues))).toBe(
      '2 of the warnings must be fixed before the diagram can be published, and Publish lists those warnings as errors.',
    );
  });

  test('an issue about a network belongs to its first switch', () => {
    const { doc, sw, alpha } = brokenSample();

    expect(issueNodeId(doc, { nodeId: alpha.id })).toBe(alpha.id);
    expect(issueNodeId(doc, { networkId: sw.switch.networkId })).toBe(sw.id);
    expect(issueNodeId(doc, { networkId: 'n-lone' })).toBe('');
    expect(issueNodeId(doc, { path: 'id' })).toBe('');
  });

  test('the canvas marks each node they are about, and says what they are', () => {
    const { doc, issues, alpha, bravo, sw } = brokenSample();
    const marks = nodeIssueSummaries(doc, issues);

    expect(marks.get(bravo.id)).toEqual({
      level: 'error',
      text:
        '1 error: duplicate hostname "alpha" (also device alpha #1). ' +
        '1 warning: interface "eth0" of "alpha" is not connected to a network and has no VLAN, so it cannot be published: connect it, or type a VLAN for it.',
    });
    expect(marks.get(alpha.id)).toEqual({
      level: 'warning',
      text:
        '2 warnings: interface "eth9" of "alpha" is not connected to a network; ' +
        'interface "eth9" of "alpha" uses VLAN "GHOST", which is not a network in this diagram.',
    });
    // A network with no switch marks no node; one with a switch marks it.
    expect([...marks.keys()].sort()).toEqual([alpha.id, bravo.id].sort());
    expect(
      nodeIssueSummaries(doc, [
        {
          level: 'warning',
          message: 'network warning',
          path: '',
          networkId: sw.switch.networkId,
        },
      ]).get(sw.id),
    ).toEqual({ level: 'warning', text: '1 warning: network warning.' });
    expect(nodeIssueSummaries(doc, []).size).toBe(0);
  });

  test('the Inspector gets the issues of what it shows, errors first', () => {
    const { doc, issues, alpha, bravo, sw, edge } = brokenSample();
    const levels = (list) => list.map((entry) => entry.level);

    const ofBravo = issuesAbout(doc, issues, { type: 'node', id: bravo.id });

    expect(ofBravo[0]).toMatchObject({
      level: 'error',
      text: 'duplicate hostname "alpha" (also device alpha #1)',
    });
    expect(levels(ofBravo)).toEqual([...levels(ofBravo)].sort());

    expect(
      issuesAbout(doc, issues, { type: 'node', id: alpha.id }).map(
        (entry) => entry.text,
      ),
    ).toContain(
      'interface "eth9" of "alpha" uses VLAN "GHOST", which is not a network in this diagram',
    );

    // A switch shows its network's issues too, not another network's.
    const ofSwitch = issuesAbout(doc, issues, { type: 'node', id: sw.id });

    expect(ofSwitch.every((entry) => entry.networkId !== 'n-lone')).toBe(true);
    expect(
      issuesAbout(doc, issues, { type: 'edge', id: edge.id }).map(
        (entry) => entry.edgeId,
      ),
    ).toEqual([edge.id]);

    // The diagram's are those about no node or connection, named in full.
    const ofDiagram = issuesAbout(doc, issues, { type: 'document' });

    expect(ofDiagram.map((entry) => entry.text)).toEqual(
      expect.arrayContaining([
        'Network LONE: network "LONE" has no switch on the canvas',
      ]),
    );
    expect(ofDiagram.some((entry) => entry.nodeId || entry.edgeId)).toBe(false);
  });

  test('the dialog groups issues by node, then connection, then diagram', () => {
    const { doc, issues, alpha, bravo, edge } = brokenSample();
    const groups = issueGroups(doc, issues);

    // In diagram order, each named as the issue texts name it.
    expect(groups.nodes.map((group) => [group.id, group.title])).toEqual(
      expect.arrayContaining([
        [alpha.id, 'Device alpha #1'],
        [bravo.id, 'Device alpha #2'],
      ]),
    );
    expect(
      groups.nodes.map((group) => group.id).indexOf(alpha.id),
    ).toBeLessThan(groups.nodes.map((group) => group.id).indexOf(bravo.id));
    expect(groups.edges).toEqual([
      expect.objectContaining({
        id: edge.id,
        title: 'Connection #1',
        issues: [
          expect.objectContaining({
            level: 'error',
            text: 'unknown network "n-gone"',
          }),
        ],
      }),
    ]);
    expect(groups.diagram.map((entry) => entry.text)).toContain(
      'Network LONE: network "LONE" has no switch on the canvas',
    );

    // Every issue is listed once.
    const listed =
      groups.nodes.flatMap((group) => group.issues).length +
      groups.edges.flatMap((group) => group.issues).length +
      groups.diagram.length;

    expect(listed).toBe(issues.length);
  });
});

describe('the one shape of an issue', () => {
  test('a message alone takes the severity given, an error by default', () => {
    expect(toIssue('experiment publication failed')).toEqual({
      severity: 'error',
      message: 'experiment publication failed',
    });
    expect(toIssue('topology t names a Builder file', 'warning')).toEqual({
      severity: 'warning',
      message: 'topology t names a Builder file',
    });
    expect(toIssue(null, 'bogus')).toEqual({ severity: 'error', message: '' });
  });

  test('an issue of the editor’s checks has its level as its severity', () => {
    const issue = {
      path: 'nodes[1].device.hostname',
      message: 'hostname is required',
      level: 'error',
      nodeId: 'n-1',
    };

    expect(toIssue(issue, 'warning')).toEqual({
      severity: 'error',
      message: 'hostname is required',
      path: 'nodes[1].device.hostname',
      nodeId: 'n-1',
    });
  });

  test('a server issue keeps its code, its ids and its field, and drops the rest', () => {
    expect(
      toIssue({
        code: 'interface-no-vlan',
        severity: 'warning',
        message: 'interface "eth0" of "web" has no VLAN',
        nodeId: 'n-2',
        edgeId: '',
        field: 'spec.network.interfaces.0.vlan',
        extra: 'x',
      }),
    ).toEqual({
      code: 'interface-no-vlan',
      severity: 'warning',
      message: 'interface "eth0" of "web" has no VLAN',
      nodeId: 'n-2',
      field: 'spec.network.interfaces.0.vlan',
    });
  });

  test('a warning about what publishing refuses is an error for publishing only', () => {
    const issue = {
      path: 'nodes[2].device.spec.network.interfaces[0]',
      message: 'no VLAN',
      level: 'warning',
      blocksPublish: true,
    };

    expect(toIssue(issue)).toMatchObject({
      severity: 'warning',
      blocksPublish: true,
    });
    expect(toIssue(issue, 'error', { publishing: true })).toMatchObject({
      severity: 'error',
      blocksPublish: true,
    });
    // A plain warning stays one.
    expect(
      toIssue({ ...issue, blocksPublish: undefined }, 'error', {
        publishing: true,
      }).severity,
    ).toBe('warning');
  });

  test('a server answer lists its errors, then its warnings, strings or objects', () => {
    expect(
      responseIssues({
        warnings: ['kept the Builder file'],
        errors: [
          'experiment publication failed',
          { severity: 'error', message: 'hostname "all"', nodeId: 'n-1' },
        ],
      }),
    ).toEqual([
      { severity: 'error', message: 'experiment publication failed' },
      { severity: 'error', message: 'hostname "all"', nodeId: 'n-1' },
      { severity: 'warning', message: 'kept the Builder file' },
    ]);
    expect(responseIssues(null)).toEqual([]);
    expect(responseIssues({ errors: 'not a list' })).toEqual([]);
  });

  test('a refusal’s issues are read, each of the severity it states or an error', () => {
    expect(
      responseIssues({
        code: 'invalid-document',
        message: 'invalid builder document',
        issues: [
          { path: 'nodes[1].device.hostname', message: 'hostname is required' },
          {
            severity: 'warning',
            message: 'interface "eth0" of "web" has no VLAN',
            nodeId: 'n-2',
          },
          'experiment publication failed',
        ],
      }),
    ).toEqual([
      {
        severity: 'error',
        message: 'hostname is required',
        path: 'nodes[1].device.hostname',
      },
      {
        severity: 'warning',
        message: 'interface "eth0" of "web" has no VLAN',
        nodeId: 'n-2',
      },
      { severity: 'error', message: 'experiment publication failed' },
    ]);
    expect(responseIssues({ issues: 'not a list' })).toEqual([]);
  });

  test('errors, warnings and issues together list each issue once', () => {
    const hostname = {
      path: 'nodes[1].device.hostname',
      message: 'hostname is required',
    };
    const vlan = {
      severity: 'warning',
      message: 'no VLAN',
      nodeId: 'n-2',
      field: 'spec.network.interfaces.0.vlan',
    };

    expect(
      responseIssues({
        errors: [
          // The issue as a message alone, as the server words it.
          'nodes[1].device.hostname: hostname is required',
          'experiment publication failed',
        ],
        warnings: ['no VLAN', 'kept the Builder file'],
        issues: [
          hostname,
          vlan,
          { ...vlan },
          // The same words about another node is another issue.
          { ...vlan, nodeId: 'n-3' },
          // And so is a message alone of another severity.
          { severity: 'warning', message: 'experiment publication failed' },
        ],
      }),
    ).toEqual([
      // A message alone gives way to the issue object that says the same,
      // which takes its place.
      { severity: 'error', ...hostname },
      { severity: 'error', message: 'experiment publication failed' },
      vlan,
      { severity: 'warning', message: 'kept the Builder file' },
      { ...vlan, nodeId: 'n-3' },
      { severity: 'warning', message: 'experiment publication failed' },
    ]);
  });
});

describe('where Go to takes an issue', () => {
  test('to the element its path starts at, with the field below it', () => {
    const { doc, alpha, bravo, sw, edge } = sampleDocument();
    const at = (path) => issueTarget(doc, { path, message: '' });

    // The sample's nodes are the switch, alpha and bravo, in that order.
    expect(at('nodes[1].device.hostname')).toEqual({
      kind: 'nodes',
      id: alpha.id,
      field: 'hostname',
    });
    expect(at('nodes[2].device.spec.network.interfaces[0].vlan')).toEqual({
      kind: 'nodes',
      id: bravo.id,
      field: 'spec.network.interfaces.0.vlan',
    });
    // The element itself, or what no form field holds, names no field.
    expect(at('nodes[2]')).toEqual({ kind: 'nodes', id: bravo.id, field: '' });
    expect(at('nodes[2].position.x').field).toBe('');
    expect(at('edges[0].label')).toEqual({
      kind: 'edges',
      id: edge.id,
      field: 'label',
    });
    // A network is shown by its first switch, whose form has its fields.
    expect(at('networks[0].name')).toEqual({
      kind: 'nodes',
      id: sw.id,
      field: 'name',
    });
    // A JSON pointer and dotted indexes are read alike.
    expect(at('/nodes/1/device/hostname')).toEqual(
      at('nodes[1].device.hostname'),
    );
    expect(at('nodes.1.device.hostname')).toEqual(
      at('nodes[1].device.hostname'),
    );
  });

  test('to the ids an issue names before its path, and nowhere the diagram lacks', () => {
    const { doc, alpha, sw, edge, network } = sampleDocument();

    expect(issueTarget(doc, { nodeId: alpha.id, field: 'hostname' })).toEqual({
      kind: 'nodes',
      id: alpha.id,
      field: 'hostname',
    });
    expect(issueTarget(doc, { edgeId: edge.id })).toEqual({
      kind: 'edges',
      id: edge.id,
      field: '',
    });
    expect(issueTarget(doc, { networkId: network.id })).toMatchObject({
      kind: 'nodes',
      id: sw.id,
    });
    expect(issueTarget(doc, { nodeId: 'gone' })).toBeNull();
    expect(issueTarget(doc, { path: 'nodes[9].device.hostname' })).toBeNull();
    expect(issueTarget(doc, { path: 'metadata.name' })).toBeNull();
    expect(issueTarget(doc, 'a message alone')).toBeNull();
  });

  test('a field the issue names is read as a data path', () => {
    const { doc } = sampleDocument();

    expect(
      issueField(doc, { field: 'spec.network.interfaces[1].address' }),
    ).toBe('spec.network.interfaces.1.address');
    expect(issueField(doc, { field: 'nodes[1].device.hostname' })).toBe(
      'hostname',
    );
    expect(
      issueField(doc, { field: 'hostname', path: 'nodes[1].device.spec' }),
    ).toBe('hostname');
    expect(issueField(doc, { path: '' })).toBe('');
  });

  test('the editor’s checks are located as validateDocument locates them', () => {
    const { doc, issues, bravo } = brokenSample();
    const duplicate = issues.find((issue) => /duplicate/.test(issue.message));

    expect(issueTarget(doc, duplicate)).toEqual({
      kind: 'nodes',
      id: bravo.id,
      field: 'hostname',
    });
    // Without its ids, its path locates it the same.
    expect(
      issueTarget(doc, { path: duplicate.path, message: duplicate.message }),
    ).toEqual(issueTarget(doc, duplicate));
  });
});

describe('the lists of checks', () => {
  test('name each issue’s element and offer Go to where the diagram has it', () => {
    const { doc, issues, bravo } = brokenSample();
    const entries = issueEntries(doc, [
      ...issues,
      'experiment publication failed',
    ]);
    const duplicate = entries.find((entry) => /duplicate/.test(entry.text));
    const lone = entries.find((entry) => /LONE/.test(entry.text));
    const server = entries.at(-1);

    expect(duplicate).toMatchObject({
      severity: 'error',
      text: 'duplicate hostname "alpha" (also device alpha #1)',
      element: 'Device alpha #2',
      target: { kind: 'nodes', id: bravo.id, field: 'hostname' },
    });
    // A network with no switch is named, but has nowhere to go.
    expect(lone).toMatchObject({ element: 'Network LONE', target: null });
    expect(server).toEqual({
      severity: 'error',
      message: 'experiment publication failed',
      text: 'experiment publication failed',
      element: '',
      target: null,
    });
    expect(goToName(duplicate)).toBe(
      'Go to device alpha #2: duplicate hostname "alpha" (also device alpha #1)',
    );
  });

  test('group errors before warnings, each in node, connection, then diagram order', () => {
    const { doc, issues, alpha, bravo, edge } = brokenSample();
    const [errors, warnings] = bySeverity(issueEntries(doc, issues), doc);
    const where = (entry) => entry.target?.id || 'diagram';
    const order = (list) => [...new Set(list.map(where))];

    expect([errors.severity, errors.label]).toEqual(['error', 'Errors']);
    expect([warnings.severity, warnings.label]).toEqual([
      'warning',
      'Warnings',
    ]);
    expect(errors.issues.every((entry) => entry.severity === 'error')).toBe(
      true,
    );
    expect(warnings.issues.every((entry) => entry.severity === 'warning')).toBe(
      true,
    );
    expect(errors.issues.length + warnings.issues.length).toBe(issues.length);
    // Bravo is after alpha among the nodes; the connection follows them, and
    // the issues about no node or connection come last.
    expect(order(errors.issues)).toEqual([bravo.id, edge.id, 'diagram']);
    expect(order(warnings.issues)).toEqual([alpha.id, bravo.id, 'diagram']);
  });

  test('without the diagram, keep the order given; level counts as severity', () => {
    const [errors, warnings] = bySeverity([
      { level: 'warning', message: 'a' },
      { severity: 'error', message: 'b' },
      { level: 'error', message: 'c' },
    ]);

    expect(errors.issues.map((entry) => entry.message)).toEqual(['b', 'c']);
    expect(warnings.issues.map((entry) => entry.message)).toEqual(['a']);
    expect(bySeverity([]).map((group) => group.issues)).toEqual([[], []]);
  });

  test('head each group with its count, the errors saying they block publishing', () => {
    const group = (severity, n) => ({
      severity,
      issues: Array.from({ length: n }, () => ({})),
    });

    expect(severityHeading(group('error', 2), { blocking: true })).toBe(
      '2 errors block publishing',
    );
    expect(severityHeading(group('error', 1), { blocking: true })).toBe(
      '1 error blocks publishing',
    );
    expect(severityHeading(group('error', 3))).toBe('3 errors');
    expect(severityHeading(group('warning', 1), { blocking: true })).toBe(
      '1 warning',
    );
  });

  test('publishing lists a warning about what it refuses as an error', () => {
    const { doc, issues } = brokenSample();
    const { errors: before, blocking } = issueCounts(issues);
    const [errors] = bySeverity(
      issueEntries(doc, issues, { publishing: true }),
      doc,
    );

    expect(blocking).toBeGreaterThan(0);
    expect(errors.issues.length).toBe(before + blocking);
  });
});
