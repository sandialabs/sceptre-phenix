// Counts the labels and notes the VM Labels dialog shows, including the topology notes.
export function tagCount(tags, topologyNotes = []) {
  const shown = Object.keys(tags).filter(
    (entry) => !entry.startsWith('__') || entry.startsWith('__notes_'),
  ).length;
  return shown + (topologyNotes?.length ?? 0);
}
