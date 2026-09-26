import { describe, expect, test } from 'vitest';

import {
  countsText,
  issueCounts,
  issueGroups,
  issueNodeId,
  issuesAbout,
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

  test('an issue about a network belongs to its first switch', () => {
    const { doc, sw, alpha } = brokenSample();

    expect(issueNodeId(doc, { nodeId: alpha.id })).toBe(alpha.id);
    expect(issueNodeId(doc, { networkId: sw.switch.networkId })).toBe(sw.id);
    expect(issueNodeId(doc, { networkId: 'n-lone' })).toBe('');
    expect(issueNodeId(doc, { path: 'id' })).toBe('');
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
