import { describe, expect, test, vi } from 'vitest';

import {
  LEGACY_TOKEN,
  actionFor,
  buildPublishIntent,
  configName,
  configNameProblem,
  describePublishResult,
  draftCanUpdate,
  hasLegacyDiagram,
  keptIncludesText,
  legacyDiagramUpdate,
  overwriteConfirmation,
  publishChecks,
  publishLabel,
  publishRefusal,
  scenarioNames,
  stageFailed,
  targetHint,
  updateBlocker,
} from '@/builder/publish.js';
import { validateDocument } from '@/builder/validate.js';

import { sampleDocument } from './fixtures.js';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

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
        'on the server with this diagram. This cannot be undone.',
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
        'A topology named "core" already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it, opened from its published diagram or published to it. ' +
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
        'An experiment named "exp" already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it or published to it. ' +
        'Enter another name to create a new experiment.',
    });
    // A topology that still has its legacy Builder diagram is refused the
    // same way for a draft that was not imported from it, and is updated by
    // the one that was.
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
        'A topology named "old" already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it, opened from its published diagram or published to it. ' +
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

  test('a stored scenario is used, not rewritten', () => {
    const { intent } = buildPublishIntent(
      {
        mode: 'topology-experiment',
        topologyName: 'core',
        experimentName: 'exp',
      },
      { scenario: { kind: 'stored', name: 'sc' } },
    );

    expect(intent.scenario).toEqual({ name: 'sc', action: 'use' });
    expect(overwriteConfirmation(intent)).toBeNull();
  });

  test('an uploaded scenario needs an explicit target', () => {
    const context = {
      scenario: { kind: 'uploaded', name: 'sc.yaml' },
      scenarios: [{ name: 'sc', digest: `sha256:${'a'.repeat(64)}` }],
    };
    const refused = buildPublishIntent(
      {
        mode: 'topology-experiment',
        topologyName: 'core',
        experimentName: 'exp',
        scenarioName: 'sc',
      },
      context,
    );

    expect(refused.intent).toBeNull();
    expect(refused.error).toMatch(/uploaded scenario/i);

    const chosen = buildPublishIntent(
      {
        topologyName: 'core',
        mode: 'topology-experiment',
        experimentName: 'exp',
        scenarioName: 'sc',
        scenarioAction: 'update',
      },
      context,
    );

    expect(chosen.intent.scenario).toEqual({
      name: 'sc',
      action: 'update',
      expectedDigest: `sha256:${'a'.repeat(64)}`,
    });
    expect(overwriteConfirmation(chosen.intent)).toMatchObject({
      title: 'Replace scenario sc?',
      confirmLabel: 'Create topology and experiment, replace scenario',
    });
  });

  test('refusals name the field they are about', () => {
    const field = (form, context = {}) =>
      buildPublishIntent(form, context).field;
    const experiment = { mode: 'topology-experiment', topologyName: 'core' };
    const uploaded = { scenario: { kind: 'uploaded', name: 'sc.yaml' } };

    expect(field({ topologyName: '' })).toBe('topologyName');
    expect(field(experiment)).toBe('experimentName');
    expect(field({ ...experiment, experimentName: 'exp' }, uploaded)).toBe(
      'scenarioName',
    );
    expect(
      field(
        { ...experiment, experimentName: 'exp', scenarioName: 'sc' },
        uploaded,
      ),
    ).toBe('scenarioAction');
    expect(field({ topologyName: 'core' })).toBe('');

    // The message is about the field it is reported on: an empty scenario
    // name is reported as missing, even with the action chosen.
    expect(
      buildPublishIntent(
        { ...experiment, experimentName: 'exp', scenarioAction: 'create' },
        uploaded,
      ),
    ).toEqual({
      intent: null,
      error: 'Enter a name for the scenario.',
      field: 'scenarioName',
    });
    // Updating a scenario the server does not have is refused on the action.
    expect(
      buildPublishIntent(
        {
          ...experiment,
          experimentName: 'exp',
          scenarioName: 'sc',
          scenarioAction: 'update',
        },
        { ...uploaded, scenarios: [] },
      ),
    ).toEqual({
      intent: null,
      error:
        'There is no scenario named "sc" to update. Choose "Create a new scenario", or enter the name of an existing scenario.',
      field: 'scenarioAction',
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
        'The topology name "Untitled topology" is not allowed. ' +
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
    expect(experiment.error).toMatch(/^The experiment name "bad name"/);

    const scenario = buildPublishIntent(
      {
        mode: 'topology-experiment',
        topologyName: 'core',
        experimentName: 'exp',
        scenarioName: 'my scenario!',
        scenarioAction: 'create',
      },
      { scenario: { kind: 'uploaded', name: 'sc.yaml' } },
    );
    expect(scenario.field).toBe('scenarioName');
    expect(scenario.error).toMatch(/^The scenario name "my scenario!"/);
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

  // Import as a copy, and Import with the included topologies combined,
  // give the document a manual source and the draft no token (see Detach in
  // types/builder/detach.go).
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

    // An experiment is updated only from its own source: creating one fills
    // in defaults, so even an unchanged republish of a created experiment is
    // refused by the server.
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

  // A topology whose diagram is read from the Builder file it names is
  // listed with no document (see listDocuments in api.js). The draft opened
  // from that file may update it; the server checks that the file and the
  // topology are still what the draft was opened from.
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

    for (const reason of [
      'Topology plant is not what its Builder file publishes, so this draft cannot update it.',
      'Topology plant or its Builder file changed after this draft was opened from the file.',
    ]) {
      expect(publishRefusal(reason, intent)).toEqual({
        message: reason,
        field: 'topologyName',
      });
    }
  });

  // The import of a topology that has a legacy Builder diagram converts
  // the diagram, and the update replaces it. No other draft read it, so the
  // server lets no other draft update the topology
  // (topologyUpdateMatchesSource).
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
      'The legacy diagram of topology old could not be read ' +
      '(the XML is not a legacy Builder diagram: its root element is <mxfile>, not <mxGraphModel>), ' +
      'so its layout was not used. Nodes were placed automatically.';
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
        source(['Left out 1 line that was not a network link.']),
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
      'A new topology will be created.',
    );
    expect(targetHint('topology', true, '')).toBe(
      'A topology with this name exists and will be updated.',
    );
    expect(targetHint('topology', true, 'source')).toBe(
      'A topology with this name already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it, opened from its published diagram or published to it. ' +
        'Enter another name to create a new topology.',
    );
    // An update of a topology that has a legacy Builder diagram says what
    // becomes of the diagram; a refusal, and a new topology, do not.
    expect(targetHint('topology', true, '', true)).toBe(
      'A topology with this name exists and will be updated. ' +
        'Its legacy Builder diagram is replaced by this diagram.',
    );
    expect(targetHint('topology', true, '', 'replaced')).toBe(
      targetHint('topology', true, '', true),
    );
    // A diagram the import could not read is not in this one: it is removed.
    expect(targetHint('topology', true, '', 'removed')).toBe(
      'A topology with this name exists and will be updated. ' +
        'Its legacy Builder diagram could not be read and is removed.',
    );
    expect(targetHint('topology', true, 'source', 'removed')).toBe(
      targetHint('topology', true, 'source'),
    );
    expect(targetHint('topology', true, 'source', true)).toBe(
      targetHint('topology', true, 'source'),
    );
    expect(targetHint('topology', false, '', true)).toBe(
      'A new topology will be created.',
    );
    expect(targetHint('experiment', true, '', true)).toBe(
      'An experiment with this name exists and will be updated.',
    );
    expect(targetHint('experiment', true, '')).toBe(
      'An experiment with this name exists and will be updated.',
    );
    expect(targetHint('experiment', true, 'source')).toMatch(
      /^An experiment with this name already exists, and this diagram cannot update it: .* Enter another name to create a new experiment\.$/,
    );
  });
});

describe('publish refusals', () => {
  const intent = {
    mode: 'topology-experiment',
    topology: { name: 'lab', action: 'create' },
    scenario: { name: 'lab', action: 'create' },
    experiment: { name: 'lab', action: 'update' },
  };

  test('a create refused because the config exists names its kind and field', () => {
    // serverReason() makes the reason a sentence.
    const exists = 'Config lab already exists; choose update explicitly.';

    // A config this draft cannot update is not promised as an update.
    expect(
      publishRefusal(exists, { topology: { name: 'lab', action: 'create' } }),
    ).toEqual({
      message:
        'A topology named "lab" already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it, opened from its published diagram or published to it. ' +
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
    // The server checks the topology, scenario and experiment in turn; the
    // refusal is about the first one with that name and action.
    expect(
      publishRefusal(
        'config lab does not exist; choose create explicitly',
        intent,
      ),
    ).toEqual({
      message:
        'The experiment "lab" no longer exists, so publishing again creates it.',
      field: 'experimentName',
    });
  });

  test('an uploaded scenario refusal points at the action to choose', () => {
    expect(
      publishRefusal('config lab already exists; choose update explicitly', {
        ...intent,
        topology: { name: 'core', action: 'update' },
      }),
    ).toEqual({
      message:
        'A scenario named "lab" already exists. Choose "Update the existing scenario", or enter another name.',
      field: 'scenarioAction',
    });
    // A stored scenario has no field in the Publish form.
    expect(
      publishRefusal('config sc does not exist; choose create explicitly', {
        topology: { name: 'core', action: 'create' },
        scenario: { name: 'sc', action: 'use' },
      }).field,
    ).toBe('');
  });

  test('a topology with a legacy Builder diagram stored meanwhile is refused like any other', () => {
    const refusal = (draft) =>
      publishRefusal(
        'Config lab already exists; choose update explicitly.',
        { topology: { name: 'lab', action: 'create' } },
        { topologies: [{ name: 'lab', builder: 'builder-xml' }], draft },
      );

    expect(refusal({})).toEqual({
      message:
        'A topology named "lab" already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it, opened from its published diagram or published to it. ' +
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
      'topology lab is not the source this draft was loaded from',
      'Topology lab is not the source this draft was loaded from.',
    ]) {
      expect(publishRefusal(reason, intent)).toEqual({
        message:
          'A topology named "lab" already exists, and this diagram cannot update it: ' +
          'the diagram was not imported from it, opened from its published diagram or published to it. ' +
          'Enter another name to create a new topology.',
        field: 'topologyName',
      });
    }

    expect(
      publishRefusal(
        'Experiment lab is not the source this draft was loaded from.',
        intent,
      ),
    ).toEqual({
      message:
        'An experiment named "lab" already exists, and this diagram cannot update it: ' +
        'the diagram was not imported from it or published to it. ' +
        'Enter another name to create a new experiment.',
      field: 'experimentName',
    });
  });

  test('other refusals keep the server reason and name the field it is about', () => {
    // The sentence a server once sent for a legacy topology is not special:
    // it is shown as any refusal that names its config.
    expect(
      publishRefusal(
        'topology lab belongs to the legacy XML Builder and cannot be updated here',
        intent,
      ),
    ).toEqual({
      message:
        'Topology lab belongs to the legacy XML Builder and cannot be updated here.',
      field: 'topologyName',
    });
    expect(
      publishRefusal('running experiment lab cannot be updated', intent).field,
    ).toBe('experimentName');
    expect(
      publishRefusal(
        'builder source Topology/lab changed after this draft was imported',
        intent,
      ).field,
    ).toBe('');
    expect(publishRefusal('', intent).message).toMatch(/Try again\.$/);
  });

  test('a topology or experiment changed since this draft published it is not overwritten', () => {
    expect(
      publishRefusal(
        'Topology lab changed after this draft published it.',
        intent,
      ),
    ).toEqual({
      message:
        'The topology "lab" changed after this diagram published it, and publishing would overwrite that change. ' +
        'Import the topology again to edit it as it is now, or enter another name to create a new topology.',
      field: 'topologyName',
      changed: 'topology/lab',
    });
    expect(
      publishRefusal(
        'Experiment lab changed after this draft published it.',
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
      'An experiment with this name changed after this diagram published it, and publishing would overwrite that change. ' +
        'Import the experiment again to edit it as it is now, or enter another name to create a new experiment.',
    );
  });

  test('a refused experiment name names its field', () => {
    expect(
      publishRefusal(
        'Experiment all is reserved: phenix uses it to mean every experiment.',
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

describe('publish checks', () => {
  test('an interface with no VLAN is an error in the Publish dialog only', () => {
    const { doc } = sampleDocument();
    const issues = validateDocument(doc);
    const checks = publishChecks(issues);
    const blocking = (list) =>
      list
        .filter((issue) => issue.level === 'error')
        .map((issue) => issue.message);

    expect(blocking(issues)).toEqual([]);
    expect(blocking(checks)).toEqual([
      'interface "eth0" of "bravo" is not connected to a network and has no VLAN, so it cannot be published: connect it, or type a VLAN for it',
    ]);
    expect(checks).toHaveLength(issues.length);
    expect(checks.filter((issue) => !issue.blocksPublish)).toEqual(
      issues.filter((issue) => !issue.blocksPublish),
    );
  });

  test('an address two interfaces use is an error in the Publish dialog only', () => {
    const { doc } = sampleDocument();

    for (const node of doc.nodes.filter((entry) => entry.kind === 'device')) {
      node.device.spec.network.interfaces[0].mac = '00:00:00:00:00:01';
    }

    const issues = validateDocument(doc);
    const errors = publishChecks(issues)
      .filter((issue) => issue.level === 'error')
      .map((issue) => issue.message);

    expect(issues.filter((issue) => issue.level === 'error')).toEqual([]);
    expect(errors).toEqual([
      'MAC address 00:00:00:00:00:01 of interface "eth0" of "alpha" is also used by interface "eth0" of "bravo"',
      expect.stringContaining('"bravo" is not connected to a network'),
      'MAC address 00:00:00:00:00:01 of interface "eth0" of "bravo" is also used by interface "eth0" of "alpha"',
    ]);
  });
});

describe('publish result', () => {
  test('describes success, partial failure and failure differently', () => {
    expect(describePublishResult({ ok: true })).toMatch(/succeeded/i);
    expect(describePublishResult({ ok: false, partial: true })).toMatch(
      /some configs were written/i,
    );
    expect(describePublishResult({ ok: false, partial: false })).toMatch(
      /no configs were written/i,
    );
    expect(describePublishResult(null)).toBe('');
  });

  test('marks the failed stages', () => {
    expect(stageFailed({ status: 'failed' })).toBe(true);
    expect(stageFailed({ status: 'error' })).toBe(true);
    expect(stageFailed({ status: 'created' })).toBe(false);
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
      /not allowed\. .*no spaces\. For example: a-b$/,
    );
    // Nothing valid to suggest.
    expect(configNameProblem('scenario', '!!')).not.toMatch(/For example/);
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
