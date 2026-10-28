import fc from 'fast-check';
import { describe, expect, test, vi } from 'vitest';

import { createBuilderApi, readPublishPreview } from '@/builder/api.js';
import {
  CONFIG_NAME_RULE,
  LEGACY_TOKEN,
  SOURCE_CHANGED,
  actionFor,
  aliasChangeText,
  buildPublishIntent,
  changedItems,
  configChangeText,
  configName,
  configNameHint,
  configNameProblem,
  configNameReason,
  describePublishResult,
  draftCanUpdate,
  hasLegacyDiagram,
  imageChangeText,
  includeChangeText,
  keptIncludesText,
  legacyDiagramUpdate,
  mergeTopologyAnnotation,
  overwriteConfirmation,
  publishChangeGroups,
  publishChangesSummary,
  publishLabel,
  publishRefusal,
  replacedScenarioConfig,
  scenarioChangeText,
  scenarioNames,
  scenarioStageHint,
  sourceChangedIssue,
  stageFailed,
  suggestedName,
  targetHint,
  updateBlocker,
} from '@/builder/publish.js';

describe('publish intent', () => {
  test('never carries the document', () => {
    const { intent } = buildPublishIntent(
      { mode: 'topology', topologyName: 'core' },
      { topologies: [] },
    );

    expect(intent).toEqual({
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });
    expect(Object.keys(intent)).not.toContain('document');
    // Creating replaces nothing, so Publish does not ask first.
    expect(overwriteConfirmation(intent)).toBeNull();
  });

  test('an existing config name becomes an update', () => {
    const { intent } = buildPublishIntent(
      {
        mode: 'topology-experiment',
        topologyName: ' core ',
        experimentName: 'exp',
      },
      {
        topologies: ['core'],
        experiments: ['exp'],
        // Generated from experiment exp, whose topology is core.
        draft: {
          source: { kind: 'experiment', name: 'exp', topology: 'core' },
        },
      },
    );

    expect(intent.topology).toEqual({ name: 'core', action: 'update' });
    expect(intent.experiment).toEqual({ name: 'exp', action: 'update' });
    // No one can undo an update, so Publish asks first, naming what it
    // replaces, and its button says what it does.
    expect(overwriteConfirmation(intent)).toEqual({
      title: 'Replace topology core and experiment exp?',
      message:
        'Publishing replaces the topology "core" and the experiment "exp" ' +
        'on the server with this diagram. You cannot undo this.',
      confirmLabel: 'Update topology and experiment',
    });
    expect(publishLabel('create', 'update')).toBe(
      'Create topology and update experiment',
    );
    expect(publishLabel('update')).toBe('Update topology');
  });

  // The server refuses these updates, so the dialog must not send them or
  // promise them.
  test('an existing name this draft cannot update is refused on its field', () => {
    const blank = { topologies: ['core'], experiments: ['exp'], draft: {} };

    expect(buildPublishIntent({ topologyName: 'core' }, blank)).toEqual({
      intent: null,
      field: 'topologyName',
      error:
        'A topology named "core" already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
        'Enter another name to create a new topology.',
    });
    expect(
      buildPublishIntent(
        {
          mode: 'topology-experiment',
          topologyName: 'new-core',
          experimentName: 'exp',
        },
        blank,
      ),
    ).toEqual({
      intent: null,
      field: 'experimentName',
      error:
        'An experiment named "exp" already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it or publish the diagram to it. ' +
        'Enter another name to create a new experiment.',
    });
    // A topology with its legacy Builder diagram is refused for a draft not
    // imported from it, and updated by the one that was.
    const legacy = [{ name: 'old', builder: 'builder-xml' }];

    expect(
      buildPublishIntent(
        { topologyName: 'old' },
        { topologies: legacy, draft: {} },
      ),
    ).toEqual({
      intent: null,
      field: 'topologyName',
      error:
        'A topology named "old" already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
        'Enter another name to create a new topology.',
    });
    expect(
      buildPublishIntent(
        { topologyName: 'old' },
        {
          topologies: legacy,
          draft: { source: { kind: 'topology', name: 'old' } },
        },
      ),
    ).toEqual({
      intent: {
        mode: 'topology',
        topology: { name: 'old', action: 'update' },
      },
      error: '',
      field: '',
    });
    // A new name is always created.
    expect(
      buildPublishIntent({ topologyName: 'new-core' }, blank).intent,
    ).toEqual({
      mode: 'topology',
      topology: { name: 'new-core', action: 'create' },
    });
  });

  test('source entries select update by name', () => {
    expect(actionFor('core', [{ name: 'core' }])).toBe('update');
  });

  test('a missing topology name is refused', () => {
    const { intent, error } = buildPublishIntent({ topologyName: '  ' }, {});

    expect(intent).toBeNull();
    expect(error).toMatch(/topology/i);
  });

  test('an experiment name is required in experiment mode', () => {
    const { intent, error } = buildPublishIntent(
      { mode: 'topology-experiment', topologyName: 'core' },
      {},
    );

    expect(intent).toBeNull();
    expect(error).toMatch(/experiment/i);
  });

  test('an experiment uses the scenario picked among those listed, by name', () => {
    const form = {
      mode: 'topology-experiment',
      topologyName: 'core',
      experimentName: 'exp',
    };
    const listed = { scenarios: ['sc-a', 'sc-b'] };

    const picked = buildPublishIntent(
      { ...form, scenarioName: 'sc-b' },
      listed,
    );

    expect(picked.intent.scenario).toEqual({ name: 'sc-b' });
    // Publishing adds the topology to every listed scenario, which replaces
    // nothing: there is nothing to confirm.
    expect(overwriteConfirmation(picked.intent)).toBeNull();

    // No scenario is no scenario key.
    const none = buildPublishIntent({ ...form, scenarioName: '' }, listed);

    expect(none.intent).not.toHaveProperty('scenario');
    expect(none.error).toBe('');

    // A topology alone picks none, whatever the form holds.
    expect(
      buildPublishIntent({ topologyName: 'core', scenarioName: 'sc-a' }, listed)
        .intent,
    ).not.toHaveProperty('scenario');
  });

  test('refusals name the field they are about', () => {
    const field = (form, context = {}) =>
      buildPublishIntent(form, context).field;
    const experiment = { mode: 'topology-experiment', topologyName: 'core' };

    expect(field({ topologyName: '' })).toBe('topologyName');
    expect(field(experiment)).toBe('experimentName');
    expect(field({ topologyName: 'core' })).toBe('');

    // A scenario the diagram does not list, as a list read before an edit
    // could leave it, is refused on the scenario's field.
    expect(
      buildPublishIntent(
        { ...experiment, experimentName: 'exp', scenarioName: 'gone' },
        { scenarios: ['sc-a'] },
      ),
    ).toEqual({
      intent: null,
      error:
        'The scenario "gone" is not one of this diagram\'s scenarios. ' +
        'Choose one of them, or No scenario.',
      field: 'scenarioName',
    });
  });

  // The server refuses these names only after it has written the document,
  // and for an experiment after the topology too.
  test('names the server would refuse are refused before publishing', () => {
    const topology = buildPublishIntent({ topologyName: 'Untitled topology' });
    expect(topology).toEqual({
      intent: null,
      field: 'topologyName',
      error:
        'The topology name "Untitled topology" is not valid: it contains a space. ' +
        'Names can use only letters, numbers, underscores (_), at signs (@), ' +
        'periods (.) and hyphens (-), with no spaces. ' +
        'For example: Untitled-topology',
    });

    const experiment = buildPublishIntent({
      mode: 'topology-experiment',
      topologyName: 'core',
      experimentName: 'bad name',
    });
    expect(experiment.field).toBe('experimentName');
    expect(experiment.error).toMatch(
      /^The experiment name "bad name" is not valid: it contains a space\. /,
    );
  });

  test('says publishing adds the topology to each listed scenario', () => {
    expect(scenarioStageHint([])).toBe('');
    expect(scenarioStageHint(['sc-a'])).toBe(
      'Publishing adds this topology to the topology annotation of the ' +
        'scenario sc-a. Experiments of the topology can then use it.',
    );
    expect(scenarioStageHint(['sc-a', 'sc-b', 'sc-c'])).toBe(
      'Publishing adds this topology to the topology annotation of each of ' +
        'the scenarios sc-a, sc-b and sc-c. Experiments of the topology ' +
        'can then use it.',
    );
  });

  test('a refused name says why: its spaces and the characters it may not use', () => {
    const rule = CONFIG_NAME_RULE;

    expect(configNameReason('a b')).toBe('it contains a space');
    expect(configNameReason('a b c')).toBe('it contains spaces');
    expect(configNameReason('a\tb')).toBe('it contains a space');
    // Each character once, in the order the name has them.
    expect(configNameReason('a/b#c/d')).toBe(
      'it contains characters that a name cannot contain: "/", "#"',
    );
    expect(configNameReason('a b/c')).toBe(
      'it contains a space and characters that a name cannot contain: "/"',
    );
    // At most five, then an ellipsis.
    expect(configNameReason('a!b"c#d$e%f&g')).toBe(
      'it contains characters that a name cannot contain: "!", """, "#", "$", "%", …',
    );
    expect(configNameReason('a!b#c$d%e^')).toBe(
      'it contains characters that a name cannot contain: "!", "#", "$", "%", "^"',
    );
    expect(configNameReason('café')).toBe(
      'it contains characters that a name cannot contain: "é"',
    );
    // A valid name, and none, have no reason.
    for (const name of ['', 'core', 'a_b@c.d-e', undefined]) {
      expect(configNameReason(name), String(name)).toBe('');
    }

    expect(configNameProblem('topology', 'a b/c')).toBe(
      `The topology name "a b/c" is not valid: it contains a space and characters that a name cannot contain: "/". ${rule} For example: a-b-c`,
    );
    expect(configNameProblem('topology', 'x y z!?')).toBe(
      `The topology name "x y z!?" is not valid: it contains spaces and characters that a name cannot contain: "!", "?". ${rule} For example: x-y-z`,
    );
    // The messages that are not about the rule stay as they were.
    expect(configNameProblem('topology', '')).toBe(
      'Enter a name for the topology.',
    );
    expect(configNameProblem('experiment', 'ALL')).toBe(
      'The experiment name "ALL" is reserved: phenix uses it to mean every experiment. Enter another name.',
    );
    expect(configNameProblem('experiment', 'lab')).toBe('');
  });

  test('a refused character that shows nothing is named by its code point', () => {
    // Characters that would look like "" when quoted, made from code points so
    // this source holds none of them.
    for (const [point, code] of [
      [0x200b, 'U+200B'],
      [0x0000, 'U+0000'],
      [0x0007, 'U+0007'],
      [0x007f, 'U+007F'],
      [0x009f, 'U+009F'],
      [0x200e, 'U+200E'],
      [0x00ad, 'U+00AD'],
      [0x2060, 'U+2060'],
      [0x0301, 'U+0301'],
      [0xfe0f, 'U+FE0F'],
      [0x3164, 'U+3164'],
      [0xe000, 'U+E000'],
      [0xe0001, 'U+E0001'],
    ]) {
      const name = `a${String.fromCodePoint(point)}b`;

      expect(configNameReason(name), code).toBe(
        `it contains characters that a name cannot contain: ${code}`,
      );
    }

    const zeroWidth = String.fromCodePoint(0x200b);

    // Visible characters stay quoted, in the order the name has them.
    expect(configNameReason(`a${zeroWidth}b/c${zeroWidth}d😀`)).toBe(
      'it contains characters that a name cannot contain: U+200B, "/", "😀"',
    );
    expect(configNameReason(`a b${zeroWidth}`)).toBe(
      'it contains a space and characters that a name cannot contain: U+200B',
    );
    expect(configNameProblem('topology', `lab${zeroWidth}1`)).toBe(
      `The topology name "lab${zeroWidth}1" is not valid: it contains characters that a name cannot contain: U+200B. ${CONFIG_NAME_RULE} For example: lab-1`,
    );
    expect(configNameHint(`lab${zeroWidth}`)).toBe(
      `This name is not valid: it contains characters that a name cannot contain: U+200B. ${CONFIG_NAME_RULE}`,
    );
  });

  test('a name field shows the rule only while its name breaks it, with why', () => {
    expect(configNameHint('two words')).toBe(
      `This name is not valid: it contains a space. ${CONFIG_NAME_RULE}`,
    );
    // The field's value is trimmed, as the name it publishes is.
    expect(configNameHint(' a/b ')).toBe(
      `This name is not valid: it contains characters that a name cannot contain: "/". ${CONFIG_NAME_RULE}`,
    );
    for (const name of ['', '   ', 'core', ' core ', null]) {
      expect(configNameHint(name), String(name)).toBe('');
    }
  });

  test('an unknown mode falls back to topology only', () => {
    const { intent } = buildPublishIntent(
      { mode: 'everything', topologyName: 'core', experimentName: 'exp' },
      {},
    );

    expect(intent.mode).toBe('topology');
    expect(intent.experiment).toBeUndefined();
  });
});

describe('which configs a draft may update', () => {
  const published = (id, target, digest = `sha256:${id}`) => ({
    id,
    kind: 'Topology',
    target,
    digest,
  });

  test('a draft updates the config it was generated from', () => {
    const topology = { source: { kind: 'topology', name: 'core' } };
    const experiment = {
      source: { kind: 'experiment', name: 'exp', topology: 'core' },
    };

    expect(draftCanUpdate('topology', 'core', topology)).toBe(true);
    expect(draftCanUpdate('topology', 'other', topology)).toBe(false);
    expect(draftCanUpdate('topology', 'core', experiment)).toBe(true);
    expect(draftCanUpdate('experiment', 'exp', experiment)).toBe(true);
    expect(draftCanUpdate('experiment', 'other', experiment)).toBe(false);
    // Not when it was generated from an upload, which names no stored config.
    const uploaded = { sourceToken: 'uploaded/Topology/core' };
    expect(
      draftCanUpdate('topology', 'core', { ...topology, ...uploaded }),
    ).toBe(false);
    expect(
      draftCanUpdate('experiment', 'exp', { ...experiment, ...uploaded }),
    ).toBe(false);
  });

  // Import as a copy and a combined import give the document a manual source
  // and the draft no token (see Detach in types/builder/detach.go).
  test('a copy or a combined import never updates the topology it was made from', () => {
    const copy = {
      sourceToken: '',
      source: { kind: 'manual', includeTopologies: ['corp-services'] },
    };
    const topologies = [
      { name: 'core' },
      { name: 'old', builder: 'builder-xml' },
    ];

    expect(draftCanUpdate('topology', 'core', copy)).toBe(false);
    expect(updateBlocker('topology', 'core', topologies, copy)).toBe('source');
    // Nor a legacy topology it was copied from, which keeps its diagram.
    expect(updateBlocker('topology', 'old', topologies, copy)).toBe('source');
    // A combined import of a config file is still an upload.
    expect(
      updateBlocker('topology', 'core', topologies, {
        sourceToken: 'uploaded/Topology/core',
        source: { kind: 'manual' },
      }),
    ).toBe('source');

    // Once it has published its own topology, it updates that one.
    const republished = {
      ...copy,
      id: 'd1',
      documents: [
        { id: 'p1', kind: 'Topology', target: 'core-copy', draftId: 'd1' },
      ],
    };

    expect(
      updateBlocker('topology', 'core-copy', topologies, republished),
    ).toBe('');
    expect(updateBlocker('topology', 'core', topologies, republished)).toBe(
      'source',
    );
  });

  test('a draft opened from a published diagram updates the topology that still points at it', () => {
    const draft = {
      sourceToken: 'builder-doc/p1',
      documents: [published('p1', 'core')],
    };

    expect(draftCanUpdate('topology', 'core', draft)).toBe(true);
    // Once the topology was published from another diagram, it no longer
    // lists p1 as current.
    expect(
      draftCanUpdate('topology', 'core', {
        ...draft,
        documents: [published('p2', 'core')],
      }),
    ).toBe(false);
    // A diagram written by hand updates the topology it published too.
    expect(
      draftCanUpdate('topology', 'core', {
        ...draft,
        source: { kind: 'manual' },
      }),
    ).toBe(true);
  });

  test('a draft updates what it published, after further edits too', () => {
    const draft = {
      id: 'd1',
      digest: 'sha256:edited',
      source: { kind: 'manual' },
      publication: {
        topologyTarget: 'core',
        experimentTarget: 'exp',
        documentId: 'p1',
      },
      documents: [published('p1', 'core')],
    };

    expect(draftCanUpdate('topology', 'core', draft)).toBe(true);
    expect(draftCanUpdate('experiment', 'exp', draft)).toBe(true);
    // The published record names the draft, when its publication aged out
    // of the draft's history.
    expect(
      draftCanUpdate('topology', 'core', {
        id: 'd1',
        documents: [{ ...published('p1', 'core'), draftId: 'd1' }],
      }),
    ).toBe(true);
    // A draft generated from an upload updates what it published.
    expect(
      draftCanUpdate('topology', 'core', {
        ...draft,
        sourceToken: 'uploaded/Topology/core',
      }),
    ).toBe(true);
    expect(draftCanUpdate('topology', 'other', draft)).toBe(false);
    expect(draftCanUpdate('experiment', 'other', draft)).toBe(false);

    // Not once the topology was published from another diagram: then the
    // experiment is no longer this draft's either.
    const superseded = { ...draft, documents: [published('p2', 'core')] };
    expect(draftCanUpdate('topology', 'core', superseded)).toBe(false);
    expect(draftCanUpdate('experiment', 'exp', superseded)).toBe(false);
  });

  test('a draft saved as a new draft from another updates what that one had published', () => {
    const draft = {
      id: 'fork',
      digest: 'sha256:edited',
      source: { kind: 'manual' },
      forked: {
        topologyTarget: 'core',
        experimentTarget: 'exp',
        documentId: 'p1',
      },
      documents: [published('p1', 'core')],
    };

    expect(draftCanUpdate('topology', 'core', draft)).toBe(true);
    expect(draftCanUpdate('experiment', 'exp', draft)).toBe(true);
    expect(draftCanUpdate('experiment', 'other', draft)).toBe(false);
    // Not once the other draft, or anything else, published it again.
    const superseded = { ...draft, documents: [published('p2', 'core')] };
    expect(draftCanUpdate('topology', 'core', superseded)).toBe(false);
    expect(draftCanUpdate('experiment', 'exp', superseded)).toBe(false);
  });

  test('an update that changes nothing is allowed', () => {
    // The topology already holds this draft's saved snapshot.
    expect(
      draftCanUpdate('topology', 'core', {
        digest: 'sha256:p1',
        documents: [published('p1', 'core')],
      }),
    ).toBe(true);
    // Edited since, it may when it published the topology (see above), not
    // otherwise.
    expect(
      draftCanUpdate('topology', 'core', {
        digest: 'sha256:edited',
        documents: [published('p1', 'core')],
      }),
    ).toBe(false);

    // Creating an experiment fills in defaults, so the server refuses even an
    // unchanged republish of one: only its own source updates it.
    expect(
      draftCanUpdate('experiment', 'exp', {
        source: { kind: 'experiment', name: 'exp' },
      }),
    ).toBe(true);
    expect(
      draftCanUpdate('experiment', 'exp', {
        digest: 'sha256:p1',
        source: { kind: 'manual' },
      }),
    ).toBe(false);
    expect(
      draftCanUpdate('experiment', 'exp', {
        sourceToken: 'uploaded/Experiment/exp',
        source: { kind: 'experiment', name: 'exp' },
      }),
    ).toBe(false);
  });

  // A topology read from its Builder file is listed with no document. The draft
  // opened from that file may update it while both are unchanged.
  test("a draft opened from a topology's Builder file updates that topology", () => {
    const digest = `sha256:${'a'.repeat(64)}`;
    const file = (target) => ({
      source: 'file',
      id: `file/${target}`,
      kind: 'Topology',
      target,
      config: `Topology/${target}`,
      path: `/phenix/topologies/${target}.builder.json`,
    });
    const draft = {
      id: 'd1',
      digest: 'sha256:edited',
      sourceToken: `builder-file/plant/${digest}`,
      source: { kind: 'manual' },
      documents: [file('plant'), file('plant-2'), published('p1', 'core')],
    };

    expect(draftCanUpdate('topology', 'plant', draft)).toBe(true);
    // Not another file's topology, not even one whose name starts the same,
    // and not a topology with a published diagram.
    expect(draftCanUpdate('topology', 'plant-2', draft)).toBe(false);
    expect(draftCanUpdate('topology', 'core', draft)).toBe(false);
    expect(
      draftCanUpdate('topology', 'plant', {
        ...draft,
        sourceToken: `builder-file/plant-2/${digest}`,
      }),
    ).toBe(false);
    // Nor from any other source: a blank draft, an upload, or the published
    // diagram of another topology.
    for (const sourceToken of [
      '',
      'uploaded/Topology/plant',
      'builder-doc/p1',
    ]) {
      expect(
        draftCanUpdate('topology', 'plant', { ...draft, sourceToken }),
      ).toBe(false);
    }
    // Once the topology is published, it is listed with that document, and
    // the token no longer counts: the draft that published it may update it.
    const stored = { ...published('p9', 'plant'), source: 'store' };

    expect(
      draftCanUpdate('topology', 'plant', { ...draft, documents: [stored] }),
    ).toBe(false);
    expect(
      draftCanUpdate('topology', 'plant', {
        ...draft,
        documents: [{ ...stored, draftId: 'd1' }],
      }),
    ).toBe(true);
    // The topology of the file is not an experiment's.
    expect(draftCanUpdate('experiment', 'plant', draft)).toBe(false);
    expect(updateBlocker('topology', 'plant', [{ name: 'plant' }], draft)).toBe(
      '',
    );
  });

  // The server's refusals of such an update name the topology, so the
  // dialog shows them on its field, as they are.
  test('a refused update of a file topology is shown on the topology field', () => {
    const intent = { topology: { name: 'plant', action: 'update' } };

    for (const [code, reason] of [
      [
        'publish.topology.file-mismatch',
        'Topology plant is not what its Builder file publishes. Thus this draft cannot update it.',
      ],
      [
        'publish.topology.file-changed',
        'Topology plant or its Builder file changed after Builder opened this draft from the file.',
      ],
    ]) {
      expect(publishRefusal({ code, reason }, intent)).toEqual({
        message: reason,
        field: 'topologyName',
      });
    }
  });

  // Importing a topology with a legacy Builder diagram converts it, so only
  // that draft may update the topology (topologyUpdateMatchesSource).
  test('a topology with a legacy Builder diagram is updated only by the draft imported from it', () => {
    const entries = [{ name: 'old', builder: 'builder-xml' }, { name: 'new' }];
    const imported = { source: { kind: 'topology', name: 'old' } };
    const fromExperiment = {
      source: { kind: 'experiment', name: 'exp', topology: 'old' },
    };

    expect(hasLegacyDiagram('old', entries)).toBe(true);
    expect(hasLegacyDiagram('new', entries)).toBe(false);
    expect(hasLegacyDiagram('old', ['old'])).toBe(false);
    expect(hasLegacyDiagram('old', undefined)).toBe(false);

    expect(updateBlocker('topology', 'old', entries, imported)).toBe('');
    expect(updateBlocker('topology', 'new', entries, imported)).toBe('source');
    expect(
      updateBlocker('topology', 'new', entries, {
        source: { kind: 'topology', name: 'new' },
      }),
    ).toBe('');

    // A draft drawn by hand, and one of another topology.
    expect(updateBlocker('topology', 'old', entries, {})).toBe('source');
    expect(
      updateBlocker('topology', 'old', entries, {
        source: { kind: 'topology', name: 'new' },
      }),
    ).toBe('source');
    // The topology as a config file, and the diagram alone, are uploads.
    expect(
      updateBlocker('topology', 'old', entries, {
        ...imported,
        sourceToken: 'uploaded/Topology/old',
      }),
    ).toBe('source');
    expect(LEGACY_TOKEN).toBe('uploaded/legacy-xml');
    expect(
      updateBlocker('topology', 'old', entries, {
        source: { kind: 'manual' },
        sourceToken: LEGACY_TOKEN,
      }),
    ).toBe('source');
    // An experiment built from the topology never held its diagram. Its
    // draft updates the topology once the diagram is gone, as before.
    expect(updateBlocker('topology', 'old', entries, fromExperiment)).toBe(
      'source',
    );
    expect(
      updateBlocker(
        'topology',
        'old',
        [{ name: 'old', builder: 'builder-doc' }],
        fromExperiment,
      ),
    ).toBe('');
    expect(updateBlocker('topology', 'old', ['old'], fromExperiment)).toBe('');
    // A topology someone changed since this draft published it stays so.
    expect(
      updateBlocker('topology', 'old', entries, {
        ...imported,
        changedTargets: ['topology/old'],
      }),
    ).toBe('changed');
  });

  // The import says so when it could not read the legacy diagram: the
  // server's sentence, as FromLegacyTopology words it, kept on the source.
  test('a legacy Builder diagram the import could not read is removed, not replaced', () => {
    const entries = [{ name: 'old', builder: 'builder-xml' }, { name: 'new' }];
    const unread =
      'The Builder could not read the legacy diagram of topology old ' +
      '(the XML is not a legacy Builder diagram: its root element is <mxfile>, not <mxGraphModel>). ' +
      'The Builder did not use its layout. The Builder placed the nodes automatically.';
    const source = (warnings, name = 'old') => ({
      kind: 'topology',
      name,
      warnings,
    });

    expect(legacyDiagramUpdate('new', entries, source([unread]))).toBe('');
    expect(legacyDiagramUpdate('old', ['old'], source([unread]))).toBe('');
    expect(legacyDiagramUpdate('old', undefined, source([unread]))).toBe('');

    expect(legacyDiagramUpdate('old', entries, source([unread]))).toBe(
      'removed',
    );
    expect(
      legacyDiagramUpdate(
        'old',
        entries,
        source(['topology node at index 1 was skipped', unread]),
      ),
    ).toBe('removed');

    // A diagram that was converted, with whatever warnings.
    expect(legacyDiagramUpdate('old', entries)).toBe('replaced');
    expect(legacyDiagramUpdate('old', entries, source(undefined))).toBe(
      'replaced',
    );
    expect(
      legacyDiagramUpdate(
        'old',
        entries,
        source([
          'The Builder did not import 1 line that was not a network link.',
        ]),
      ),
    ).toBe('replaced');
    // The warning of another topology's import says nothing of this one.
    expect(legacyDiagramUpdate('old', entries, source([unread], 'new'))).toBe(
      'replaced',
    );
    expect(
      legacyDiagramUpdate('old', entries, {
        kind: 'manual',
        warnings: [unread],
      }),
    ).toBe('replaced');
  });

  test('the name hint promises an update only when there can be one', () => {
    expect(targetHint('topology', false, 'source')).toBe(
      'Publishing creates a new topology.',
    );
    expect(targetHint('topology', true, '')).toBe(
      'A topology with this name exists. Publishing updates it.',
    );
    expect(targetHint('topology', true, 'source')).toBe(
      'A topology with this name already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
        'Enter another name to create a new topology.',
    );
    // An update of a topology that has a legacy Builder diagram says what
    // becomes of the diagram. A refusal, and a new topology, do not.
    expect(targetHint('topology', true, '', true)).toBe(
      'A topology with this name exists. Publishing updates it. ' +
        'This diagram replaces its legacy Builder diagram.',
    );
    expect(targetHint('topology', true, '', 'replaced')).toBe(
      targetHint('topology', true, '', true),
    );
    // A diagram the import could not read is not in this one: it is removed.
    expect(targetHint('topology', true, '', 'removed')).toBe(
      'A topology with this name exists. Publishing updates it. ' +
        'Builder could not read its legacy Builder diagram. Publishing removes it.',
    );
    expect(targetHint('topology', true, 'source', 'removed')).toBe(
      targetHint('topology', true, 'source'),
    );
    expect(targetHint('topology', true, 'source', true)).toBe(
      targetHint('topology', true, 'source'),
    );
    expect(targetHint('topology', false, '', true)).toBe(
      'Publishing creates a new topology.',
    );
    expect(targetHint('experiment', true, '', true)).toBe(
      'An experiment with this name exists. Publishing updates it.',
    );
    expect(targetHint('experiment', true, '')).toBe(
      'An experiment with this name exists. Publishing updates it.',
    );
    expect(targetHint('experiment', true, 'source')).toMatch(
      /^An experiment with this name already exists\. This diagram cannot update it\. .* Enter another name to create a new experiment\.$/,
    );
  });
});

describe('the name the Publish dialog suggests', () => {
  const imported = {
    source: { kind: 'topology', name: 'riverside-water' },
  };
  const topologies = [{ name: 'riverside-water' }, { name: 'water' }];

  test('it offers the topology the draft was imported from', () => {
    expect(
      suggestedName('topology', 'Riverside-Water', topologies, imported),
    ).toBe('riverside-water');
    // Not once the field holds it, with or without spaces around it.
    expect(
      suggestedName('topology', ' riverside-water ', topologies, imported),
    ).toBe('');
    // Not when the server has no topology of that name to update.
    expect(
      suggestedName('topology', 'Riverside-Water', [{ name: 'water' }], {
        ...imported,
      }),
    ).toBe('');
    // Not for an upload, which names no stored config.
    expect(
      suggestedName('topology', 'Riverside-Water', topologies, {
        ...imported,
        sourceToken: 'uploaded/Topology/riverside-water',
      }),
    ).toBe('');
    expect(suggestedName('topology', 'Blank', topologies, {})).toBe('');
  });

  test('the topology it last published to comes first', () => {
    const own = (id, target) => ({
      id,
      kind: 'Topology',
      target,
      draftId: 'd1',
    });
    const draft = {
      ...imported,
      id: 'd1',
      publication: { topologyTarget: 'water', documentId: 'p1' },
      documents: [own('p1', 'water')],
    };

    expect(
      suggestedName('topology', 'Riverside-Water', topologies, draft),
    ).toBe('water');
    // The topology of the publication its draft was forked from is next.
    expect(
      suggestedName('topology', 'x', topologies, {
        ...imported,
        id: 'd2',
        forked: { topologyTarget: 'water', documentId: 'p1' },
        documents: [{ id: 'p1', kind: 'Topology', target: 'water' }],
      }),
    ).toBe('water');
  });

  test('it offers the topology of the published diagram or Builder file the draft was opened from', () => {
    const opened = {
      sourceToken: 'builder-doc/p1',
      documents: [{ id: 'p1', kind: 'Topology', target: 'water' }],
    };

    expect(suggestedName('topology', 'Water', topologies, opened)).toBe(
      'water',
    );
    // Until the published diagrams are read, it cannot tell.
    expect(
      suggestedName('topology', 'Water', topologies, {
        ...opened,
        documents: [],
      }),
    ).toBe('');

    const file = {
      sourceToken: 'builder-file/water/sha256:abc',
      documents: [{ kind: 'Topology', target: 'water', source: 'file' }],
    };

    expect(suggestedName('topology', 'Water', topologies, file)).toBe('water');
  });

  test('it offers no topology that this draft may not update', () => {
    // Someone else changed it after this draft published it.
    expect(
      suggestedName('topology', 'x', topologies, {
        ...imported,
        changedTargets: ['topology/riverside-water'],
      }),
    ).toBe('');

    // A legacy diagram that only an import of the topology may replace. The
    // next candidate is offered instead.
    const legacy = [
      { name: 'old', builder: 'builder-xml' },
      { name: 'riverside-water' },
    ];
    const draft = {
      ...imported,
      id: 'd1',
      publication: { topologyTarget: 'old', documentId: 'p1' },
      documents: [{ id: 'p1', kind: 'Topology', target: 'old', draftId: 'd1' }],
    };

    expect(updateBlocker('topology', 'old', legacy, draft)).toBe('source');
    expect(suggestedName('topology', 'x', legacy, draft)).toBe(
      'riverside-water',
    );
  });

  test('it offers the experiment the draft published to or came from', () => {
    const draft = {
      id: 'd1',
      publication: {
        topologyTarget: 'water',
        experimentTarget: 'lab',
        documentId: 'p1',
      },
      documents: [
        { id: 'p1', kind: 'Topology', target: 'water', draftId: 'd1' },
      ],
    };
    const experiments = [{ name: 'lab' }, { name: 'exp' }];

    expect(suggestedName('experiment', '', experiments, draft)).toBe('lab');
    expect(suggestedName('experiment', 'lab', experiments, draft)).toBe('');
    // Not once its topology no longer holds this draft's diagram.
    expect(
      suggestedName('experiment', '', experiments, {
        ...draft,
        documents: [],
      }),
    ).toBe('');

    // A draft imported from an experiment offers it, and its topology.
    const fromExperiment = {
      source: { kind: 'experiment', name: 'exp', topology: 'water' },
    };

    expect(suggestedName('experiment', '', experiments, fromExperiment)).toBe(
      'exp',
    );
    expect(suggestedName('topology', 'Water', topologies, fromExperiment)).toBe(
      'water',
    );
  });

  test('every name it offers exists and is one this draft may update', () => {
    const names = ['a', 'b', 'c', 'd'];
    const name = fc.constantFrom(...names, '', 'bad name');
    const kind = fc.constantFrom('topology', 'experiment');
    const entry = fc.record({
      name: fc.constantFrom(...names),
      builder: fc.constantFrom(undefined, 'builder-xml'),
    });
    const published = fc.option(
      fc.record({
        topologyTarget: name,
        experimentTarget: name,
        documentId: fc.constantFrom('p1', 'p2'),
      }),
      { nil: undefined },
    );
    const draft = fc.record({
      id: fc.constantFrom('d1', 'd2'),
      sourceToken: fc.constantFrom(
        '',
        'uploaded/Topology/a',
        'builder-doc/p1',
        'builder-file/b/sha256:x',
      ),
      source: fc.option(
        fc.record({
          kind: fc.constantFrom('topology', 'experiment', 'manual'),
          name,
          topology: name,
        }),
        { nil: null },
      ),
      publication: published,
      forked: published,
      documents: fc.array(
        fc.record({
          id: fc.constantFrom('p1', 'p2', 'p3'),
          kind: fc.constantFrom('Topology', 'Experiment'),
          target: fc.constantFrom(...names),
          draftId: fc.constantFrom('d1', 'd2', ''),
          source: fc.constantFrom(undefined, 'file'),
        }),
        { maxLength: 4 },
      ),
      changedTargets: fc.array(
        fc.tuple(kind, fc.constantFrom(...names)).map((pair) => pair.join('/')),
        { maxLength: 2 },
      ),
    });

    fc.assert(
      fc.property(
        kind,
        name,
        fc.array(entry, { maxLength: 5 }),
        draft,
        (target, current, entries, record) => {
          const offered = suggestedName(target, current, entries, record);

          if (offered) {
            expect(updateBlocker(target, offered, entries, record)).toBe('');
            expect(actionFor(offered, entries)).toBe('update');
            expect(offered).not.toBe(current.trim());
          }
        },
      ),
    );
  });
});

describe('a source config that changed', () => {
  test('the dry run lists the refusal that the dialog warns of', () => {
    const changed = { code: SOURCE_CHANGED, message: 'this diagram was …' };

    expect(SOURCE_CHANGED).toBe('publish.source.changed');
    expect(
      sourceChangedIssue([{ code: 'publish.topology.exists' }, changed]),
    ).toBe(changed);
    expect(
      sourceChangedIssue([{ code: 'publish.topology.exists' }]),
    ).toBeNull();
    expect(sourceChangedIssue(undefined)).toBeNull();
  });
});

describe('publish refusals', () => {
  const intent = {
    mode: 'topology-experiment',
    topology: { name: 'lab', action: 'create' },
    scenario: { name: 'lab' },
    experiment: { name: 'lab', action: 'update' },
  };

  // serverRefusal() reads the code and makes the reason a sentence.
  const exists = {
    code: 'publish.topology.exists',
    reason: 'Config lab already exists. Choose update explicitly.',
  };

  test('a create refused because the config exists names its kind and field', () => {
    // A config this draft cannot update is not promised as an update.
    expect(
      publishRefusal(exists, { topology: { name: 'lab', action: 'create' } }),
    ).toEqual({
      message:
        'A topology named "lab" already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
        'Enter another name to create a new topology.',
      field: 'topologyName',
    });
    expect(
      publishRefusal(
        exists,
        { topology: { name: 'lab', action: 'create' } },
        { draft: { source: { kind: 'topology', name: 'lab' } } },
      ),
    ).toEqual({
      message:
        'A topology named "lab" already exists. Publish again to update it, or enter another name to create a new topology.',
      field: 'topologyName',
    });
    // The code names the config: the server checks the topology and the
    // experiment in turn, and reports the first it refuses.
    expect(
      publishRefusal(
        {
          code: 'publish.experiment.missing',
          reason: 'config lab does not exist. Choose create explicitly',
        },
        intent,
      ),
    ).toEqual({
      message:
        'The experiment "lab" no longer exists. Publishing again creates it.',
      field: 'experimentName',
    });
  });

  test('a scenario refusal points at the scenario field only for the experiment’s', () => {
    const picked = { ...intent, scenario: { name: 'lab' } };
    const scenario = (code, reason, name) =>
      publishRefusal({ code, reason, scenario: name }, picked);

    expect(
      scenario(
        'publish.scenario.missing',
        'Scenario lab does not exist.',
        'lab',
      ),
    ).toEqual({
      message: 'Scenario lab does not exist.',
      field: 'scenarioName',
    });
    expect(
      scenario(
        'publish.scenario.not-listed',
        'Scenario lab is not one of the scenarios this draft lists.',
        'lab',
      ).field,
    ).toBe('scenarioName');
    // Another listed scenario has no field in the Publish form.
    expect(
      scenario(
        'publish.scenario.missing',
        'Scenario other does not exist.',
        'other',
      ).field,
    ).toBe('');
    // A scenario is never created or updated as a whole, so a refusal of a
    // config's create or update is never about it.
    for (const code of [
      'publish.topology.exists',
      'publish.experiment.exists',
      'publish.experiment.missing',
    ]) {
      expect(
        publishRefusal(
          {
            code,
            reason: 'config lab already exists. Choose update explicitly',
          },
          { ...picked, topology: { name: 'core', action: 'update' } },
        ).field,
      ).not.toBe('scenarioName');
    }
  });

  test('a topology with a legacy Builder diagram stored meanwhile is refused like any other', () => {
    const refusal = (draft) =>
      publishRefusal(
        exists,
        { topology: { name: 'lab', action: 'create' } },
        { topologies: [{ name: 'lab', builder: 'builder-xml' }], draft },
      );

    expect(refusal({})).toEqual({
      message:
        'A topology named "lab" already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
        'Enter another name to create a new topology.',
      field: 'topologyName',
    });
    // The draft imported from it may update it, which publishing again does.
    expect(refusal({ source: { kind: 'topology', name: 'lab' } })).toEqual({
      message:
        'A topology named "lab" already exists. Publish again to update it, or enter another name to create a new topology.',
      field: 'topologyName',
    });
  });

  test('an update the server refuses asks for another name', () => {
    for (const reason of [
      'topology lab is not the source of this draft',
      'Topology lab is not the source of this draft.',
    ]) {
      expect(
        publishRefusal({ code: 'publish.topology.not-source', reason }, intent),
      ).toEqual({
        message:
          'A topology named "lab" already exists. This diagram cannot update it. ' +
          'You did not import the diagram from it, open the diagram from its published diagram or publish the diagram to it. ' +
          'Enter another name to create a new topology.',
        field: 'topologyName',
      });
    }

    expect(
      publishRefusal(
        {
          code: 'publish.experiment.not-source',
          reason: 'Experiment lab is not the source of this draft.',
        },
        intent,
      ),
    ).toEqual({
      message:
        'An experiment named "lab" already exists. This diagram cannot update it. ' +
        'You did not import the diagram from it or publish the diagram to it. ' +
        'Enter another name to create a new experiment.',
      field: 'experimentName',
    });
  });

  test('other refusals keep the server reason and name the field their code is about', () => {
    // What only publishing refuses is about the topology.
    expect(
      publishRefusal(
        {
          code: 'publish.blocked',
          reason:
            'phenix cannot publish topology lab: interface "eth0" of device "web" has no VLAN',
        },
        intent,
      ),
    ).toEqual({
      message:
        'Phenix cannot publish topology lab: interface "eth0" of device "web" has no VLAN.',
      field: 'topologyName',
    });
    expect(
      publishRefusal(
        {
          code: 'publish.experiment.running',
          reason: 'phenix cannot update running experiment lab',
        },
        intent,
      ).field,
    ).toBe('experimentName');
    expect(
      publishRefusal(
        {
          code: 'publish.source.changed',
          reason:
            'this diagram was made from topology lab, and that topology changed on the server after the diagram was made',
        },
        intent,
      ).field,
    ).toBe('');
    // A refusal without a code keeps its reason, on no field: the words are
    // not read for a config.
    expect(
      publishRefusal(
        {
          reason:
            'topology lab belongs to the legacy XML Builder and cannot be updated here',
        },
        intent,
      ),
    ).toEqual({
      message:
        'Topology lab belongs to the legacy XML Builder and cannot be updated here.',
      field: '',
    });
    expect(publishRefusal({}, intent).message).toMatch(/Try again\.$/);
  });

  test('a topology or experiment changed since this draft published it is not overwritten', () => {
    expect(
      publishRefusal(
        {
          code: 'publish.topology.changed',
          reason: 'Topology lab changed after this draft published it.',
        },
        intent,
      ),
    ).toEqual({
      message:
        'The topology "lab" changed after this diagram published it. Publishing would overwrite that change. ' +
        'Import the topology again to edit it as it is now, or enter another name to create a new topology.',
      field: 'topologyName',
      changed: 'topology/lab',
    });
    expect(
      publishRefusal(
        {
          code: 'publish.experiment.changed',
          reason: 'Experiment lab changed after this draft published it.',
        },
        intent,
      ),
    ).toMatchObject({ field: 'experimentName', changed: 'experiment/lab' });

    // From then on the dialog refuses the name itself, with the same reason,
    // and offers to create a config of another name instead.
    const draft = { id: 'd1', changedTargets: ['experiment/lab'] };
    const entries = [{ name: 'lab' }];

    expect(updateBlocker('experiment', 'lab', entries, draft)).toBe('changed');
    expect(updateBlocker('topology', 'lab', entries, draft)).toBe('source');
    expect(targetHint('experiment', true, 'changed')).toBe(
      'An experiment with this name changed after this diagram published it. Publishing would overwrite that change. ' +
        'Import the experiment again to edit it as it is now, or enter another name to create a new experiment.',
    );
  });

  test('a refused experiment name names its field', () => {
    expect(
      publishRefusal(
        {
          code: 'publish.experiment.reserved',
          reason:
            'Experiment all is reserved: phenix uses it to mean every experiment.',
        },
        { ...intent, experiment: { name: 'all', action: 'create' } },
      ).field,
    ).toBe('experimentName');
    expect(configNameProblem('experiment', 'ALL')).toBe(
      'The experiment name "ALL" is reserved: phenix uses it to mean every experiment. Enter another name.',
    );
    expect(configNameProblem('topology', 'all')).toBe('');
  });
});

// phenix stores a topology with an interface VLAN of "", and minimega
// refuses it when the experiment starts.
describe('includes the diagram keeps without showing their nodes', () => {
  test('the summary names them, by reference', () => {
    expect(keptIncludesText(['site-b'])).toBe(
      ' The published topology also includes site-b by reference.',
    );
    expect(keptIncludesText(['site-b', 'site-c'])).toBe(
      ' The published topology also includes site-b and site-c by reference.',
    );
    expect(keptIncludesText(['a', 'b', 'c'])).toBe(
      ' The published topology also includes a, b and c by reference.',
    );
  });

  test('a diagram that includes nothing adds nothing', () => {
    expect(keptIncludesText()).toBe('');
    expect(keptIncludesText([])).toBe('');
    expect(keptIncludesText(null)).toBe('');
    expect(keptIncludesText('site-b')).toBe('');
    expect(keptIncludesText([''])).toBe('');
  });

  test('a long list names eight and counts the rest', () => {
    const many = Array.from({ length: 20 }, (_, index) => `t${index + 1}`);

    expect(keptIncludesText(many)).toBe(
      ' The published topology also includes t1, t2, t3, t4, t5, t6, t7, t8 and 12 more by reference.',
    );
    expect(keptIncludesText(many.slice(0, 8))).toBe(
      ' The published topology also includes t1, t2, t3, t4, t5, t6, t7 and t8 by reference.',
    );
  });
});

describe('publish result', () => {
  test('describes success, partial failure and failure differently', () => {
    expect(describePublishResult({ ok: true })).toMatch(/succeeded/i);
    expect(describePublishResult({ ok: false, partial: true })).toMatch(
      /publish wrote some configs/i,
    );
    expect(describePublishResult({ ok: false, partial: false })).toMatch(
      /it wrote no configs/i,
    );
    expect(describePublishResult(null)).toBe('');
  });

  test('says how to finish a partial publication that can be finished', () => {
    expect(
      describePublishResult({ ok: false, partial: true, retry: true }),
    ).toBe(
      'Published with failures. Publish wrote some configs. The list below shows the failed stages. Publish again with the same choices to finish. The server skips the steps that are already done.',
    );
    expect(
      describePublishResult({ ok: false, partial: true, retry: false }),
    ).not.toMatch(/publish again/i);
  });

  test('marks the failed stages', () => {
    expect(stageFailed({ status: 'failed' })).toBe(true);
    expect(stageFailed({ status: 'error' })).toBe(true);
    expect(stageFailed({ status: 'created' })).toBe(false);
  });
});

describe('what publishing changes', () => {
  // A preview as readPublishPreview reads the server's answer.
  const changes = {
    topology: { name: 'riverside', action: 'create' },
    experiment: { name: 'lab', action: 'update' },
    includes: [
      { name: 'plant-a', change: 'removed' },
      { name: 'plant-b', change: 'added' },
      { name: 'plant-c', change: 'kept' },
    ],
    scenarios: [
      { name: 'water-ops', change: 'annotate' },
      { name: 'drill', change: 'unchanged' },
    ],
    images: [
      {
        name: 'ubuntu.qc2',
        change: 'added',
        devices: ['web-1', 'web-2'],
        onServer: false,
      },
      {
        name: 'centos.qc2',
        change: 'kept',
        devices: ['a', 'b', 'c', 'd', 'e', 'f', 'g'],
        onServer: true,
      },
      { name: 'old.qc2', change: 'removed', devices: ['db'], onServer: null },
    ],
    vlanAliases: [
      { name: 'ot', from: 101, to: 120, change: 'changed' },
      { name: 'it', from: null, to: 7, change: 'added' },
      { name: 'dmz', from: 5, to: null, change: 'removed' },
      { name: 'corp', from: 9, to: 9, change: 'kept' },
    ],
  };

  test('says what publishing does to each config', () => {
    expect(configChangeText('Topology', changes.topology)).toBe(
      'Creates Topology config riverside',
    );
    expect(configChangeText('Experiment', changes.experiment)).toBe(
      'Updates Experiment config lab',
    );
    expect(
      configChangeText('Topology', { name: 'riverside', action: 'unchanged' }),
    ).toBe(
      'Topology config riverside is unchanged: it already holds this diagram',
    );
  });

  test('words every line of every group', () => {
    expect(
      publishChangeGroups(changes).flatMap((group) => group.lines),
    ).toEqual([
      'Creates Topology config riverside',
      'Updates Experiment config lab',
      'Removes included topology plant-a',
      'Adds included topology plant-b',
      'Keeps included topology plant-c',
      'Adds topology riverside to Scenario water-ops',
      'Scenario drill already names topology riverside',
      'Disk image ubuntu.qc2 is new (web-1 and web-2 use it), but the server does not have it',
      'Disk image centos.qc2 is still in use (a, b, c, d, e and 2 more use it), and the server has it',
      'Disk image old.qc2 is no longer in use (db used it)',
      'VLAN alias for network ot changes from 101 to 120',
      'Sets the VLAN alias for network it to 7',
      'Removes VLAN alias 5 for network dmz',
      'VLAN alias for network corp stays 9',
    ]);
  });

  test('says whether the server has an image only when that is known', () => {
    const image = { name: 'vyos.qc2', change: 'added', devices: ['fw'] };

    expect(imageChangeText(image)).toBe(
      'Disk image vyos.qc2 is new (fw uses it)',
    );
    expect(imageChangeText({ ...image, onServer: true })).toBe(
      'Disk image vyos.qc2 is new (fw uses it), and the server has it',
    );
    // Nothing is said of a removed image's presence.
    expect(
      imageChangeText({ ...image, change: 'removed', onServer: false }),
    ).toBe('Disk image vyos.qc2 is no longer in use (fw used it)');
  });

  test('words each item of the other lists', () => {
    expect(includeChangeText({ name: 'a', change: 'added' })).toBe(
      'Adds included topology a',
    );
    expect(scenarioChangeText({ name: 's', change: 'annotate' }, 't')).toBe(
      'Adds topology t to Scenario s',
    );
    expect(
      aliasChangeText({ name: 'ot', from: null, to: 0, change: 'added' }),
    ).toBe('Sets the VLAN alias for network ot to 0');
  });

  test('groups the lines by what they are about, leaving out empty lists', () => {
    expect(
      publishChangeGroups(changes).map(({ key, title, lines }) => [
        key,
        title,
        lines.length,
      ]),
    ).toEqual([
      ['configs', 'Configs', 2],
      ['includes', 'Included topologies', 3],
      ['scenarios', 'Scenarios', 2],
      ['images', 'Disk images', 3],
      ['aliases', 'VLAN aliases', 4],
    ]);

    const topologyOnly = {
      topology: { name: 'riverside', action: 'unchanged' },
      experiment: null,
      includes: [],
      scenarios: [],
      images: [],
      vlanAliases: [],
    };

    expect(publishChangeGroups(topologyOnly)).toEqual([
      {
        key: 'configs',
        title: 'Configs',
        lines: [
          'Topology config riverside is unchanged: it already holds this diagram',
        ],
      },
    ]);
    expect(changedItems(topologyOnly)).toBe(0);
    expect(publishChangeGroups(null)).toEqual([]);
  });

  test('announces what happens to the configs and how many lines follow', () => {
    expect(publishChangesSummary(changes)).toBe(
      'What publishing changes: Creates Topology config riverside. ' +
        'Updates Experiment config lab. 12 more lines below.',
    );
    expect(
      publishChangesSummary({
        topology: { name: 'riverside', action: 'create' },
        includes: [],
        scenarios: [],
        images: [],
      }),
    ).toBe(
      'What publishing changes: Creates Topology config riverside. ' +
        'Nothing outside the Topology changes.',
    );
  });

  test('reads the server answer to a dry run', () => {
    expect(
      readPublishPreview({
        data: {
          status: 'preview',
          changes: {
            topology: { name: 'riverside', action: 'update' },
            includes: [{ name: 'plant-a', change: 'kept' }, { change: 'x' }],
            scenarios: [],
            images: [{ name: 'ubuntu.qc2', change: 'kept', devices: null }],
          },
          warnings: [{ code: 'publish.file.unchanged', message: 'kept' }],
          errors: [],
        },
      }),
    ).toEqual({
      changes: {
        topology: { name: 'riverside', action: 'update' },
        experiment: null,
        includes: [{ name: 'plant-a', change: 'kept' }],
        scenarios: [],
        images: [
          { name: 'ubuntu.qc2', change: 'kept', devices: [], onServer: null },
        ],
        vlanAliases: [],
      },
      warningIssues: [
        {
          code: 'publish.file.unchanged',
          severity: 'warning',
          message: 'kept',
        },
      ],
      errorIssues: [],
    });

    // A publication the server would refuse has no changes.
    const refused = readPublishPreview({
      data: {
        status: 'preview',
        changes: null,
        warnings: [],
        errors: [
          {
            code: 'publish.topology.exists',
            message: 'config riverside already exists',
          },
        ],
      },
    });

    expect(refused.changes).toBeNull();
    expect(refused.errorIssues).toEqual([
      {
        code: 'publish.topology.exists',
        severity: 'error',
        message: 'config riverside already exists',
      },
    ]);
  });

  test('a dry run sends the intent with dryRun and no If-Match', async () => {
    const post = vi.fn(async () => ({
      data: { status: 'preview', changes: null, warnings: [], errors: [] },
    }));
    const preview = await createBuilderApi({ post }).previewPublish('d1', {
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
    });

    expect(post).toHaveBeenCalledWith('builder/drafts/d1/publish', {
      mode: 'topology',
      topology: { name: 'core', action: 'create' },
      dryRun: true,
    });
    expect(preview.changes).toBeNull();
  });
});

describe('config names', () => {
  test('a diagram name becomes a valid config name', () => {
    expect(configName('Untitled topology')).toBe('Untitled-topology');
    expect(configName('  lab #2 (copy) ')).toBe('lab-2-copy');
    expect(configName('core_net@site.v2')).toBe('core_net@site.v2');
    expect(configName('')).toBe('');
    expect(configName(undefined)).toBe('');
  });

  test('explains an empty or invalid name and suggests a valid one', () => {
    expect(configNameProblem('topology', 'core-01')).toBe('');
    expect(configNameProblem('topology', '')).toBe(
      'Enter a name for the topology.',
    );
    expect(configNameProblem('experiment', 'a b')).toMatch(
      /not valid: it contains a space\. .*no spaces\. For example: a-b$/,
    );
    // Nothing valid to suggest.
    expect(configNameProblem('scenario', '!!')).not.toMatch(/For example/);
    expect(configNameProblem('scenario', '!!')).toMatch(
      /^The scenario name "!!" is not valid: it contains characters that a name cannot contain: "!"\. /,
    );
  });
});

describe('sources', () => {
  test('accepts scenario names as strings or entries', () => {
    expect(scenarioNames(['a', { name: 'b' }, {}, null])).toEqual(['a', 'b']);
  });

  test('chooses create or update from what the server has', () => {
    expect(actionFor('core', ['core'])).toBe('update');
    expect(actionFor(' core ', ['core'])).toBe('update');
    expect(actionFor('other', ['core'])).toBe('create');
    expect(actionFor('other')).toBe('create');
  });
});

describe('replacing a stored scenario', () => {
  test('topology annotations merge with no stored topology lost', () => {
    // The stored annotation stays as it is. Each name of the uploaded one
    // it does not name follows after a comma.
    expect(mergeTopologyAnnotation('plant , mill', 'mill,dock, yard')).toBe(
      'plant , mill,dock,yard',
    );
    expect(mergeTopologyAnnotation('', 'plant')).toBe('plant');
    expect(mergeTopologyAnnotation('plant', '')).toBe('plant');
    // A name that only contains another is not it.
    expect(mergeTopologyAnnotation('xplant', 'plant,plant')).toBe(
      'xplant,plant',
    );
  });

  const uploaded = (annotations) => ({
    apiVersion: 'phenix.sandia.gov/v2',
    kind: 'Scenario',
    metadata: annotations ? { name: 'ntp', annotations } : { name: 'ntp' },
    spec: { apps: [{ name: 'new' }] },
  });
  const stored = (annotations) => ({
    apiVersion: 'phenix.sandia.gov/v2',
    kind: 'Scenario',
    metadata: {
      name: 'ntp',
      created: '2026-01-01T00:00:00Z',
      ...(annotations ? { annotations } : {}),
    },
    spec: { apps: [{ name: 'old' }] },
  });

  const replace = (kept, given) =>
    replacedScenarioConfig(stored(kept), uploaded(given));

  test('the uploaded spec replaces the stored one, which keeps its annotations', () => {
    const replaced = replace(
      { topology: 'plant', owner: 'ops', keep: 'yes' },
      { topology: 'dock', owner: 'lab' },
    );

    expect(replaced).toEqual(
      uploaded({ topology: 'plant,dock', owner: 'lab', keep: 'yes' }),
    );

    // Only one of them has a topology annotation: it is kept as it is.
    expect(replace({ topology: 'plant' })).toEqual(
      uploaded({ topology: 'plant' }),
    );
    expect(replace(null, { topology: 'dock' })).toEqual(
      uploaded({ topology: 'dock' }),
    );

    // Neither has annotations: none are sent.
    expect(replace()).toEqual(uploaded());
  });
});
