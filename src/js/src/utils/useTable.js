import { reactive, watch } from 'vue';
import { loadPaginate, savePaginate } from '@/utils/paginatePref.js';

// useTable holds a Buefy <b-table>'s pagination state (whether it pages, the
// page, page size and default sort direction) for the table views.
//
// It is a Composition API composable, but integrates with the Options API
// components in this project: values returned from setup() are exposed on the
// component instance, so `this.table` is available in methods, computed and
// template.
//
// Each call returns an independent table, so views with more than one table
// (e.g. a VMs table and a files table) can call useTable() once per table.
//
// Pass { name } to remember the table's Paginate toggle in this browser (see
// paginatePref.js); a table without a name, and so without a toggle, stays
// unpaginated. Pass { fields } for the rest of a table's state, such as the
// total and sort column of a table the server pages.
export function useTable(options = {}) {
  const name = options.name;
  const table = reactive({
    isPaginated: name ? loadPaginate(name) : false,
    perPage: options.perPage ?? 10,
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

  return { table };
}
