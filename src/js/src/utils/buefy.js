import {
  Autocomplete,
  BDialog,
  BDropdown,
  BModal,
  BTable,
  BTableColumn,
  BTooltip,
  Breadcrumb,
  Button,
  Checkbox,
  Collapse,
  ConfigProgrammatic,
  Dialog,
  Dropdown,
  Field,
  Icon,
  Input,
  Loading,
  Modal,
  Navbar,
  Notification,
  Numberinput,
  Progress,
  Radio,
  Select,
  Switch,
  Table,
  Tabs,
  Tag,
  Toast,
  Tooltip,
  Upload,
} from 'buefy';
import { setNotificationApp } from '@/utils/notify.js';
import { returnFocus } from '@/utils/returnFocus.js';
import { TOAST_DURATION } from '@/utils/toast.js';

// Register only the Buefy components the UI uses: app.use(Buefy) pulls every
// component into the entry chunk. Add a plugin here before using a new b-* tag
// (test/buefy.test.js checks this); heavy components used by one view
// (colorpicker, datetimepicker, slider) are registered locally in that view so
// they load with its chunk.
const buefyPlugins = [
  Autocomplete,
  Breadcrumb,
  Button,
  Checkbox,
  Collapse,
  Dialog,
  Dropdown,
  Field,
  Icon,
  Input,
  Loading,
  Modal,
  Navbar,
  Notification,
  Numberinput,
  Progress,
  Radio,
  Select,
  Switch,
  Table,
  Tabs,
  Tag,
  Toast,
  Tooltip,
  Upload,
];

export function installBuefy(app) {
  setNotificationApp(app);
  ConfigProgrammatic.setOptions({
    defaultIconComponent: 'font-awesome-icon',
    defaultIconPack: 'fas',
    defaultProgrammaticPromise: true,
    defaultToastDuration: TOAST_DURATION,
    // queued, a new toast empties the notice area, lasting notices included
    defaultNoticeQueue: false,
  });
  // Buefy has no config default for the modal close button's name, so the X
  // button is unnamed unless every modal passes close-button-aria-label. Set
  // the prop default before the first render; b-modal and $buefy.modal share
  // this component.
  BModal.props.closeButtonAriaLabel = { type: String, default: 'Close' };
  // $buefy.dialog renders aria-modal="false" with no role, which ARIA forbids
  BDialog.props.ariaRole = { type: String, default: 'alertdialog' };
  BDialog.props.ariaModal = { type: Boolean, default: true };
  for (const component of [BModal, BDialog, BDropdown]) {
    component.mixins = [...(component.mixins ?? []), returnFocus];
  }
  BModal.props.ariaRole = { type: String, default: 'dialog' };
  BModal.props.ariaModal = { type: Boolean, default: true };
  // phenix is desktop only: a zoomed page keeps its tables and their header
  // sorts rather than Buefy's mobile cards, whose sort controls are unnamed
  BTable.props.mobileCards = { type: Boolean, default: false };
  BTableColumn.props.thAttrs = { type: Function, default: sortableHeading };
  // a tooltip shows on keyboard focus as on hover (WCAG 1.4.13)
  BTooltip.props.triggers = { type: Array, default: () => ['hover', 'focus'] };
  buefyPlugins.forEach((plugin) => app.use(plugin));
}

// A sortable heading's attributes: Tab reaches it, Enter or Space sorts by it
// as a click does, and aria-sort says how the table is sorted.
function sortableHeading(column) {
  if (!column.sortable) return {};
  const table = column.$table;
  const sorted = table && table.currentSortColumn === column;
  return {
    tabindex: 0,
    'aria-sort': sorted ? (table.isAsc ? 'ascending' : 'descending') : 'none',
    onKeydown(event) {
      if (event.key !== 'Enter' && event.key !== ' ') return;
      event.preventDefault();
      event.currentTarget.click();
    },
  };
}
