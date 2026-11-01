import { reactive, watch } from 'vue';
import { usePhenixStore } from '@/store.js';
import { loadPaginate, savePaginate } from '@/utils/paginatePref.js';

// A Buefy <b-table>'s pagination state (paging, page, page size and default
// sort) for an Options API view's setup(), as `this.table`; call it per table.
// { name } remembers the Paginate toggle in this browser (see paginatePref.js);
// without one the table stays unpaginated. { fields } adds other state.
export function useTable(options = {}) {
  const name = options.name;
  const table = reactive({
    isPaginated: name ? loadPaginate(name) : false,
    perPage: 10,
    currentPage: 1,
    isPaginationSimple: true,
    paginationSize: 'is-small',
    defaultSortDirection: options.defaultSortDirection ?? 'asc',
    ...options.fields,
  });

  if (name) {
    watch(
      () => table.isPaginated,
      (on) => savePaginate(name, on),
    );
  }

  function storageKey() {
    return `${usePhenixStore().username}.lastPaginate`;
  }

  // Persist the current pagination preference for this user.
  function changePaginate() {
    localStorage.setItem(storageKey(), table.isPaginated);
  }

  // Restore the saved pagination preference for this user (if any).
  function restorePaginate() {
    const saved = localStorage.getItem(storageKey());
    if (saved !== null) {
      table.isPaginated = saved === 'true';
    }
  }

  return { table, changePaginate, restorePaginate };
}
