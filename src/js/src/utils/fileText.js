// Text of an experiment file for the file viewer: JSON is indented so it is
// readable, anything else is shown as it is.
export function fileText(name, text) {
  if (typeof text !== 'string') return '';
  if (!name.toLowerCase().endsWith('.json')) return text;

  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
