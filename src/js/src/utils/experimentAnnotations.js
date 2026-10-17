// Annotations a new experiment can be given from the create card: node
// annotations added to every VM of the experiment's copy of its topology, and
// annotations on the experiment itself.

// The node annotations phenix's own default apps read (see the phenix skill's
// references/annotations.md). `type` picks the value input and how the value
// is sent: the server rejects a value of another type for the keys it checks
// (all of these but phenix/startup-via-cc).
export const NODE_ANNOTATIONS = [
  {
    key: 'phenix/default-apps',
    type: 'boolean',
    value: false,
    summary: 'Skip the default apps',
    description:
      'false skips the ntp, serial, startup and vrouter default apps for ' +
      'every VM; scenario apps still run.',
  },
  {
    key: 'phenix/startup-autotunnel',
    type: 'list',
    placeholder: '8080, 8080:9090, 8080:10.0.0.5:9090',
    summary: 'Forward ports once VMs boot',
    description:
      'Port forwards the startup app creates for each VM once it boots, ' +
      'separated by commas: sport, sport:dport or sport:dhost:dport.',
  },
  {
    key: 'phenix/startup-via-cc',
    type: 'boolean',
    value: true,
    summary: 'Run startup scripts over miniccc',
    description:
      "true delivers and runs each VM's startup scripts over minimega's " +
      'miniccc agent instead of injecting them into its disk.',
  },
  {
    key: 'vrouter/vyos-password',
    type: 'password',
    summary: 'VyOS router login password',
    description:
      'The login password written into VyOS router configs in place of the ' +
      'default vyos.',
  },
  {
    key: 'vrouter/enable-ssh',
    type: 'string',
    placeholder: 'IF0 or 10.0.0.1',
    summary: 'Enable SSH on routers',
    description:
      "Enables SSH on routers, listening on this interface's address or on " +
      'this IP address.',
  },
];

// The experiment annotations phenix reads that are safe to set at creation.
export const EXPERIMENT_ANNOTATIONS = [
  {
    key: 'phenix.workflow/tags',
    type: 'string',
    placeholder: 'key1=value1,key2=value2',
    summary: 'Tags for SCORCH run info',
    description:
      'key=value pairs, separated by commas, recorded in the info file ' +
      'SCORCH writes for each run.',
  },
];

// Experiment annotations the card may not set, and why. The server rejects
// topology and scenario, and sets the branch from workflow_branch.
export const MANAGED_EXPERIMENT_ANNOTATIONS = {
  topology: 'Set by phēnix from the topology',
  scenario: 'Set by phēnix from the scenario',
  'phenix.workflow/branch': 'Set by the Git Workflow Branch Name field',
};

export const CUSTOM_NODE_ANNOTATION = {
  type: 'text',
  summary: 'Any key and value',
  description:
    'true, false and JSON lists or objects keep their type; anything else ' +
    'is text.',
};

export const CUSTOM_EXPERIMENT_ANNOTATION = {
  type: 'text',
  summary: 'Any key and value',
  description: 'Apps can read it from the experiment metadata.',
};

let nextRowId = 0;

// A new row of an annotation list, for a catalog entry (its key is fixed) or
// a custom annotation (no key).
export function annotationRow(entry) {
  return {
    id: nextRowId++,
    known: Boolean(entry.key),
    key: entry.key ?? '',
    type: entry.type,
    value: entry.value ?? '',
    placeholder: entry.placeholder ?? '',
    description: entry.description,
  };
}

// The value of a custom node annotation: the same rule as the CLI's
// --node-annotation flag.
export function customNodeValue(text) {
  const trimmed = text.trim();
  if (trimmed === 'true') return true;
  if (trimmed === 'false') return false;
  if (trimmed.startsWith('[') || trimmed.startsWith('{')) {
    try {
      return JSON.parse(trimmed);
    } catch {
      // not JSON after all, so it stays text
    }
  }
  return text;
}

// An annotation's value as text: strings as they are, anything else as JSON.
export function annotationText(value) {
  return typeof value == 'string' ? value : JSON.stringify(value);
}

function fitsType(type, value) {
  switch (type) {
    case 'boolean':
      return typeof value == 'boolean';
    case 'list':
      return (
        Array.isArray(value) && value.every((item) => typeof item == 'string')
      );
    default:
      return typeof value == 'string';
  }
}

// The rows that edit annotations a node already has. A known annotation
// whose value is not of its type becomes a custom row, so it can be fixed.
// Each row remembers its value, so one left as it was keeps its type when
// saved (a custom row's number would otherwise become text).
export function annotationRowsFrom(annotations, catalog, custom) {
  return Object.keys(annotations ?? {})
    .sort((a, b) => a.localeCompare(b))
    .map((key) => {
      const value = annotations[key];
      const entry = catalog.find((e) => e.key == key);
      let row;
      if (entry && fitsType(entry.type, value)) {
        row = annotationRow(entry);
        row.value = entry.type == 'list' ? value.join(', ') : value;
      } else {
        row = { ...annotationRow(custom), key, value: annotationText(value) };
      }
      row.original = { text: row.value, value };
      return row;
    });
}

function nodeValue(row) {
  if (row.original && row.value === row.original.text) {
    return row.original.value;
  }
  switch (row.type) {
    case 'boolean':
      return row.value;
    case 'list':
      return row.value
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean);
    case 'text':
      return customNodeValue(row.value);
    default:
      return row.value;
  }
}

// The create request's node_annotations for a list of rows.
export function nodeAnnotationsPayload(rows) {
  return Object.fromEntries(
    rows.map((row) => [row.key.trim(), nodeValue(row)]),
  );
}

// The create request's (experiment) annotations for a list of rows.
export function experimentAnnotationsPayload(rows) {
  return Object.fromEntries(
    rows.map((row) => [row.key.trim(), String(row.value)]),
  );
}

// What is wrong with each row, or null, in the order of the rows.
export function annotationErrors(rows, managed = {}) {
  const seen = new Set();
  return rows.map((row) => {
    const key = row.key.trim();
    if (!key) return 'Enter a key';
    if (seen.has(key)) return `${key} is already in the list`;
    seen.add(key);
    if (Object.hasOwn(managed, key)) return managed[key];
    if (row.type != 'boolean' && !String(row.value).trim()) {
      return 'Enter a value';
    }
    return null;
  });
}
