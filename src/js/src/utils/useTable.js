import { reactive } from 'vue';
import { usePhenixStore } from '@/store.js';

// useTable centralizes the Buefy <b-table> pagination state and the per-user
// "remember pagination" preference that every table view in phenix duplicated.
//
// It is a Composition API composable, but integrates with the Options API
// components in this project: values returned from setup() are exposed on the
// component instance, so `this.table`, `this.changePaginate()` and
// `this.restorePaginate()` are all available in methods, computed and template.
//
// Each call returns an independent table, so views with more than one table
// (e.g. a VMs table and a files table) can call useTable() once per table.
//
// The pagination preference is keyed off the logged-in user from the Pinia
// store (store.username). Previously some views read store.username while others
// read localStorage.getItem('user'); useTable unifies that on the store.
export function useTable(options = {}) {
  const table = reactive({
    isPaginated: false,
    perPage: options.perPage ?? 10,
    currentPage: 1,
    isPaginationSimple: true,
    paginationSize: 'is-small',
    defaultSortDirection: options.defaultSortDirection ?? 'asc',
  });

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
