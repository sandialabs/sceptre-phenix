// The search history, oldest first, with this search as its newest, dropping the
// oldest once full; it keeps no search under four characters but "dnb".
export function addSearch(history, search, limit = 10) {
  const term = search.trim();
  if (term.length < 4 && term !== 'dnb') return history;
  return [...history.filter((kept) => kept !== term), term].slice(-limit);
}
