import { BModal, BTableColumn } from 'buefy';

// buefyA11y adds app-wide accessibility defaults to Buefy. Modals and dialogs
// get role="dialog", aria-modal, a name from their title or message, a named
// close button, and focus return to their opener (WCAG 4.1.2, 2.4.3).
// Sortable table headers get focus, Enter/Space sort, and aria-sort (2.1.1).
// Dialogs extend BModal, so they get the BModal changes. Install after Buefy.

let labelCount = 0;

function setDefault(component, name, value) {
  const prop = component.props[name];
  const options =
    prop && typeof prop === 'object' && !Array.isArray(prop)
      ? prop
      : { type: prop };
  component.props[name] = { ...options, default: value };
}

function labelFromTitle(root) {
  if (!(root instanceof HTMLElement)) return;
  const el = root.matches('.modal') ? root : root.querySelector('.modal');
  if (!el) return;
  if (el.hasAttribute('aria-label') || el.hasAttribute('aria-labelledby')) {
    return;
  }
  // An untitled $buefy.dialog has only its message to name it.
  const label =
    el.querySelector('.modal-card-title') ??
    (el.matches('.dialog')
      ? el.querySelector('.modal-card-body .media-content > p')
      : null);
  if (!label) return;
  if (!label.id) {
    labelCount += 1;
    label.id = `modal-label-${labelCount}`;
  }
  el.setAttribute('aria-labelledby', label.id);
}

export const modalFocusReturn = {
  created() {
    if (this.isActive) this.a11yRememberOpener();
  },
  watch: {
    isActive(active) {
      if (active) {
        this.a11yRememberOpener();
        this.$nextTick(() => labelFromTitle(this.$el));
      } else {
        this.$nextTick(() => this.a11yRestoreFocus());
      }
    },
  },
  beforeUnmount() {
    this.a11yRestoreFocus();
  },
  methods: {
    a11yRememberOpener() {
      const opener = document.activeElement;
      const inside = this.$el instanceof Node && this.$el.contains(opener);
      if (opener && opener !== document.body && !inside) {
        this.a11yOpener = opener;
      }
    },
    a11yRestoreFocus() {
      const opener = this.a11yOpener;
      this.a11yOpener = null;
      if (!opener || !opener.isConnected) return;
      const current = document.activeElement;
      const lost =
        !current ||
        current === document.body ||
        (this.$el instanceof Node && this.$el.contains(current));
      if (lost) opener.focus();
    },
  },
};

function sortOnKey(event) {
  if (event.key !== 'Enter' && event.key !== ' ') return;
  if (event.target !== event.currentTarget) return;
  event.preventDefault();
  event.currentTarget.click();
}

// The default `th-attrs` of every b-table-column. A view that sets its own
// `th-attrs` replaces it.
export function sortableHeaderAttrs(column) {
  if (!column.sortable) return {};
  const attrs = { tabindex: 0, onKeydown: sortOnKey };
  const table = column.$table;
  if (table && !table.sortMultiple && table.currentSortColumn === column) {
    attrs['aria-sort'] = table.isAsc ? 'ascending' : 'descending';
  }
  return attrs;
}

export default {
  install() {
    setDefault(BModal, 'ariaRole', 'dialog');
    setDefault(BModal, 'ariaModal', true);
    setDefault(BModal, 'closeButtonAriaLabel', 'Close');
    BModal.mixins = [...(BModal.mixins ?? []), modalFocusReturn];
    setDefault(BTableColumn, 'thAttrs', sortableHeaderAttrs);
  },
};
