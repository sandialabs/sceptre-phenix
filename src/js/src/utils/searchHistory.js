// Whether a search is worth keeping: four characters or more, or "dnb", the
// search for VMs set not to boot.
export const keptSearch = (term) => term.length >= 4 || term === 'dnb';

// The search history, oldest first, with this search as its newest, dropping the
// oldest once full; it keeps only searches keptSearch keeps.
export function addSearch(history, search, limit = 10) {
  const term = search.trim();
  if (!keptSearch(term)) return history;
  return [...history.filter((kept) => kept !== term), term].slice(-limit);
}
