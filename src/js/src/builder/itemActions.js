// The buttons that act on one item: label, icon (a key of iconPaths.js) and
// tone, drawn by BuilderActionButton.vue. Edit and Open are blue, Delete red.

const TONE_CLASSES = Object.freeze({
  default: '',
  primary: 'builder-button--primary',
  danger: 'builder-button--danger',
});

export const ITEM_ACTIONS = Object.freeze({
  edit: Object.freeze({ label: 'Edit', icon: 'pencil', tone: 'primary' }),
  delete: Object.freeze({ label: 'Delete', icon: 'trash', tone: 'danger' }),
  publish: Object.freeze({
    label: 'Publish',
    icon: 'publish',
    tone: 'default',
  }),
  duplicate: Object.freeze({
    label: 'Duplicate',
    icon: 'copy',
    tone: 'default',
  }),
  open: Object.freeze({ label: 'Open', icon: 'folder-open', tone: 'primary' }),
  view: Object.freeze({ label: 'View', icon: 'eye', tone: 'default' }),
  download: Object.freeze({
    label: 'Download',
    icon: 'download',
    tone: 'default',
  }),
});

/** The label, icon and tone of an item action, or null for an unknown name. */
export function itemAction(name) {
  return Object.hasOwn(ITEM_ACTIONS, name) ? ITEM_ACTIONS[name] : null;
}

/** The class of the button's color for a tone: '' for the default. */
export function toneClass(tone) {
  return Object.hasOwn(TONE_CLASSES, tone) ? TONE_CLASSES[tone] : '';
}
