import {
  Autocomplete,
  BModal,
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
  });
  // Buefy has no config default for the modal close button's name, so the X
  // button is unnamed unless every modal passes close-button-aria-label. Set
  // the prop default before the first render; b-modal and $buefy.modal share
  // this component.
  BModal.props.closeButtonAriaLabel = { type: String, default: 'Close' };
  buefyPlugins.forEach((plugin) => app.use(plugin));
}
