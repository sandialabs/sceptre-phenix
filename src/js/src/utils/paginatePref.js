import { readPref, writePref } from '@/utils/prefs.js';

// Each table's last Paginate setting, by table name, shared by every user of
// this browser. Tables start unpaginated until someone turns it on. Kept in
// localStorage only, so it survives logout and reloads but never reaches the
// server.
const key = (table) => `phenix.paginate.${table}`;

export const loadPaginate = (table) => readPref(key(table)) === 'true';

export const savePaginate = (table, on) => writePref(key(table), Boolean(on));
