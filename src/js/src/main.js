import { createApp } from 'vue';
import { createPinia } from 'pinia';

import './assets/main.scss';
import Buefy from 'buefy';

/* import the fontawesome core */
import { library } from '@fortawesome/fontawesome-svg-core';
import {
  FontAwesomeIcon,
  FontAwesomeLayers,
  FontAwesomeLayersText,
} from '@fortawesome/vue-fontawesome';

//import all the icons we use.
// just adding 'fas' to the library adds almost a megabyte to the page bundle
// prettier-ignore
import {
    faTrash, faDownload, faEdit, faUpload, faPlus, faWindowClose, faFileDownload, faInfoCircle, faHeartbeat,
    faKey, faQuestionCircle, faFire, faChevronDown, faChevronUp, faArrowUp, faSearch, faExclamationCircle,
    faTag, faBolt, faDesktop, faFileAlt, faNetworkWired, faPlay, faBars, faExclamationTriangle, faCircleNodes, faStop,
    faPlayCircle, faStopCircle, faPause, faDatabase, faSave, faCamera, faHistory, faSkullCrossbones, faUndoAlt, 
    faSyncAlt, faPowerOff, faPencil, faArrowRight, faCompactDisc, faCheckCircle, faHdd, faMinus, faTerminal,
    faPaintbrush, faTv, faCircle, faRefresh, faCaretDown, faTimesCircle
} from '@fortawesome/free-solid-svg-icons'

// prettier-ignore
library.add(
    faTrash, faDownload, faEdit, faUpload, faPlus, faWindowClose, faFileDownload, faInfoCircle, faHeartbeat,
    faKey, faQuestionCircle, faFire, faChevronDown, faChevronUp, faArrowUp, faSearch, faExclamationCircle,
    faTag, faBolt, faDesktop, faFileAlt, faNetworkWired, faPlay, faBars, faExclamationTriangle, faCircleNodes, faStop,
    faPlayCircle, faStopCircle, faPause, faDatabase, faSave, faCamera, faHistory, faSkullCrossbones, faUndoAlt, 
    faSyncAlt, faPowerOff, faPencil, faArrowRight, faCompactDisc, faCheckCircle, faHdd, faMinus, faTerminal,
    faPaintbrush, faTv, faCircle, faRefresh, faCaretDown, faTimesCircle
)

import App from './App.vue';
import router from './router';

const app = createApp(App);

app.component('font-awesome-icon', FontAwesomeIcon);
app.component('font-awesome-layers', FontAwesomeLayers);
app.component('font-awesome-layers-text', FontAwesomeLayersText);

const pinia = createPinia();
app.use(pinia);
app.use(router);
app.use(Buefy, {
  defaultIconComponent: 'font-awesome-icon',
  defaultIconPack: 'fas',
});

app.mount('#app');
