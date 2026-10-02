// Wording for messages about a count or a list of things, so one VM reads
// "the web VM" rather than "the VMs web".

// the singular or plural form of a word for a count: VM or VMs
export function pluralWord(count, singular, plural = `${singular}s`) {
  return count == 1 ? singular : plural;
}

// a count with its noun: "1 VM", "3 VMs"
export function plural(count, singular, pluralForm) {
  return `${count} ${pluralWord(count, singular, pluralForm)}`;
}

// a list in prose: "a", "a and b", "a, b, and c"
export function listText(items) {
  const list = [...items];
  if (list.length <= 1) return list.join('');
  if (list.length == 2) return `${list[0]} and ${list[1]}`;
  return `${list.slice(0, -1).join(', ')}, and ${list.at(-1)}`;
}

// names of one kind of thing: "the web VM", "the VMs web and db"
export function namedList(names, singular, pluralForm) {
  const list = Array.isArray(names) ? names : [names];
  if (list.length == 1) return `the ${list[0]} ${singular}`;
  return `the ${pluralWord(list.length, singular, pluralForm)} ${listText(list)}`;
}

// a sentence's first letter in upper case
export function upperFirst(text) {
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : text;
}
