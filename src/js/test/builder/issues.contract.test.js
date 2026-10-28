import { describe, expect, test } from 'vitest';

import {
  bySeverity,
  countsText,
  edgeIssueSummaries,
  goToName,
  issueCounts,
  issueEntries,
  issueField,
  issueNodeId,
  issueTarget,
  issuesAbout,
  issuesNotInChecks,
  nodeIssueSummaries,
  publishingText,
  responseIssues,
  severityHeading,
  toIssue,
} from '@/builder/issues.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

// The sample with a duplicate hostname (an error), a VLAN naming no network,
// a link to an unknown network, a switchless network and one clashing with EXP.
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
      'Warnings do not stop you from publishing the diagram.',
    );
    expect(publishingText({ errors: 1, warnings: 2 })).toBe(
      'You must fix the errors before you can publish the diagram.',
    );
    expect(publishingText({ errors: 0, warnings: 1, blocking: 1 })).toBe(
      'You must fix the warning before you can publish the diagram. Publish lists that warning as an error.',
    );
    expect(publishingText({ errors: 0, warnings: 3, blocking: 1 })).toBe(
      'You must fix 1 of the warnings before you can publish the diagram. Publish lists that warning as an error.',
    );
    expect(publishingText({ errors: 2, warnings: 2, blocking: 2 })).toBe(
      'You must fix the errors and the warnings before you can publish the diagram. Publish lists those warnings as errors.',
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
      `IP address 10.0.0.5 of interface "eth0" of "${hostname}" on VLAN "EXP" is also the address of interface "eth0" of "${other}"`;

    expect(nodeIssueSummaries(doc, issues).get(alpha.id)).toEqual({
      level: 'warning',
      text: `1 warning: ${of('alpha', 'bravo')}.`,
    });
    expect(nodeIssueSummaries(doc, issues).get(bravo.id).text).toContain(
      of('bravo', 'alpha'),
    );
    const blocking = (id) =>
      issuesAbout(doc, issues, { type: 'node', id })
        .filter((entry) => entry.blocksPublish)
        .map((entry) => entry.text);

    expect(blocking(alpha.id)).toEqual([of('alpha', 'bravo')]);
    expect(blocking(bravo.id)).toEqual([of('bravo', 'alpha')]);
    // Bravo's unconnected eth0 is the third warning.
    expect(issueCounts(issues)).toEqual({
      errors: 0,
      warnings: 3,
      blocking: 2,
    });
    expect(publishingText(issueCounts(issues))).toBe(
      'You must fix 2 of the warnings before you can publish the diagram. Publish lists those warnings as errors.',
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
        '1 warning: interface "eth0" of "alpha" is not connected to a network and has no VLAN. Thus you cannot publish the interface. Connect it, or type a VLAN for it.',
    });
    expect(marks.get(alpha.id)).toEqual({
      level: 'warning',
      text:
        '2 warnings: interface "eth9" of "alpha" is not connected to a network. ' +
        'interface "eth9" of "alpha" uses VLAN "GHOST", which is not a network in this diagram.',
    });
    // A network with no switch marks no node. One with a switch marks it.
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

  test('the canvas marks each connection they are about, and says what they are', () => {
    const { doc, issues, edge } = brokenSample();
    const marks = edgeIssueSummaries(doc, issues);
    const own = issues.filter((entry) => entry.edgeId === edge.id);

    expect(own.length).toBeGreaterThan(0);
    expect([...marks.keys()]).toEqual([edge.id]);
    expect(marks.get(edge.id)).toEqual({
      level: 'error',
      text: '1 error: unknown network "n-gone".',
    });
    // An edge's issues are not a node's.
    expect(nodeIssueSummaries(doc, own).size).toBe(0);
    expect(edgeIssueSummaries(doc, []).size).toBe(0);

    // An error marks it with a cross, before its warnings.
    const both = edgeIssueSummaries(doc, [
      { level: 'warning', message: 'second', path: '', edgeId: 'x' },
      { level: 'error', message: 'first', path: '', edgeId: 'x' },
    ]);

    expect(both.get('x')).toEqual({
      level: 'error',
      text: '1 error: first. 1 warning: second.',
    });
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
        'Network LONE: network "LONE" has no switch on the canvas. Add a switch for it, change each VLAN that names it, or use Remove network',
      ]),
    );
    expect(ofDiagram.some((entry) => entry.nodeId || entry.edgeId)).toBe(false);
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
      message: 'a device needs a hostname',
      level: 'error',
      nodeId: 'n-1',
    };

    expect(toIssue(issue, 'warning')).toEqual({
      severity: 'error',
      message: 'a device needs a hostname',
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
          {
            path: 'nodes[1].device.hostname',
            message: 'a device needs a hostname',
          },
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
        message: 'a device needs a hostname',
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
      message: 'a device needs a hostname',
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
          'nodes[1].device.hostname: a device needs a hostname',
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

  test('the ruleset and address warnings go to the field that fixes them', () => {
    const { doc, alpha, bravo, sw } = sampleDocument();
    const next = JSON.parse(JSON.stringify(doc));
    const node = (id) => next.nodes.find((entry) => entry.id === id);
    const target = (code) =>
      issueTarget(
        next,
        validateDocument(next).find((entry) => entry.code === code),
      );

    next.networks[0].subnet = '10.1.0.0/24';
    next.networks[0].gateway = '10.1.0.254';
    Object.assign(node(alpha.id).device.spec.network.interfaces[0], {
      address: '10.9.0.1',
      ruleset_in: 'gone',
    });
    node(alpha.id).device.spec.type = 'Router';
    node(bravo.id).device.spec.network.rulesets = [{ name: 'in' }];

    expect(target('interface.ruleset.unknown')).toEqual({
      kind: 'nodes',
      id: alpha.id,
      field: 'spec.network.interfaces.0.ruleset_in',
    });
    expect(target('interface.ip.outside-subnet')).toEqual({
      kind: 'nodes',
      id: alpha.id,
      field: 'spec.network.interfaces.0.address',
    });
    expect(target('device.rulesets.ignored')).toEqual({
      kind: 'nodes',
      id: bravo.id,
      field: 'spec.type',
    });
    expect(target('network.gateway.unowned')).toEqual({
      kind: 'nodes',
      id: sw.id,
      field: 'gateway',
    });
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
    // Bravo is after alpha among the nodes. The connection follows them, and
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

  test('a Builder refusal lists its issues with their codes, and Go to each', () => {
    const { doc, alpha } = sampleDocument();
    const message =
      'IP address 10.0.0.5 on VLAN "EXP" is used by interface "eth0" of device "alpha" and interface "eth0" of device "bravo"';
    // The body of the 422 the server answers a publish with when only
    // publishing refuses the topology (builderCodedError in web/builder.go).
    const body = {
      cause: `validating topology projection: ${message}`,
      message: `phenix cannot publish topology Sample: ${message}`,
      code: 'publish.blocked',
      issues: [
        {
          code: 'interface.ip.shared',
          severity: 'error',
          message,
          path: 'nodes[1].device.spec.network.interfaces[0].address',
          nodeId: alpha.id,
          field: 'spec.network.interfaces.0.address',
        },
      ],
    };
    const [errors, warnings] = bySeverity(
      issueEntries(doc, responseIssues(body)),
      doc,
    );

    expect(warnings.issues).toEqual([]);
    expect(errors.issues).toHaveLength(1);
    expect(errors.issues[0]).toMatchObject({
      code: 'interface.ip.shared',
      severity: 'error',
      message,
      element: 'Device alpha',
      target: {
        kind: 'nodes',
        id: alpha.id,
        field: 'spec.network.interfaces.0.address',
      },
    });
    expect(errors.issues[0].text).toContain('10.0.0.5 on VLAN "EXP"');
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

describe('what a dry run of a publication adds to the checks', () => {
  // The sample's bravo is not connected, so its eth0 has no VLAN: the
  // checks list it, as the Publish dialog does, and the server refuses it.
  function sample() {
    const { doc, alpha, bravo } = sampleDocument();
    const checks = issueEntries(doc, validateDocument(doc), {
      publishing: true,
    });
    const index = doc.nodes.findIndex((node) => node.id === bravo.id);
    const missing = {
      code: 'interface.vlan.missing',
      severity: 'error',
      message:
        'interface "eth0" of device "bravo" has no VLAN: connect it to a network, or type a VLAN for it',
      path: `nodes[${index}].device.spec.network.interfaces[0]`,
      nodeId: bravo.id,
      field: 'spec.network.interfaces.0.vlan',
    };

    return { doc, alpha, bravo, checks, missing };
  }

  test('leaves out a problem the checks list, by its code and element, whatever its words and severity', () => {
    const { doc, checks, missing } = sample();

    expect(checks.map((check) => check.code)).toContain(missing.code);
    expect(issuesNotInChecks(doc, [missing], checks)).toEqual([]);
    // Located by its path alone, it is about the same device.
    expect(
      issuesNotInChecks(doc, [{ ...missing, nodeId: undefined }], checks),
    ).toEqual([]);
    // A warning of the dry run the checks list is left out too.
    expect(
      issuesNotInChecks(doc, [{ ...missing, severity: 'warning' }], checks),
    ).toEqual([]);
  });

  test('keeps a problem of another element, one only the server finds, and one with no code', () => {
    const { doc, alpha, checks, missing } = sample();
    const issues = [
      { ...missing, nodeId: alpha.id, path: '' },
      {
        code: 'publish.include.clash',
        severity: 'error',
        message: 'hostname "alpha" is also a device of included topology plant',
      },
      {
        code: 'publish.scenario.missing',
        severity: 'error',
        message: 'scenario water-ops does not exist',
      },
      {
        code: '',
        severity: 'warning',
        message: 'the Builder file of the topology is not changed',
      },
    ];

    expect(issuesNotInChecks(doc, issues, checks)).toEqual(issues);
    expect(issuesNotInChecks(doc, issues)).toEqual(issues);
    expect(issuesNotInChecks(doc, [], checks)).toEqual([]);
  });

  test('a problem about the whole diagram matches a check about the whole diagram', () => {
    const { doc, bravo } = sample();
    const checks = [
      { code: 'publish.file.unchanged', severity: 'warning', message: 'a' },
      {
        code: 'node.hostname.phenix',
        severity: 'warning',
        message: 'b',
        nodeId: bravo.id,
      },
    ];
    const diagram = {
      code: 'publish.file.unchanged',
      severity: 'warning',
      message: 'in other words',
    };
    const unlocated = {
      code: 'node.hostname.phenix',
      severity: 'warning',
      message: 'c',
    };
    const located = { ...unlocated, nodeId: bravo.id };

    expect(
      issuesNotInChecks(doc, [diagram, unlocated, located], checks),
    ).toEqual([unlocated]);
  });
});
