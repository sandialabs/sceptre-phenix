import { createApp } from 'vue';
import { createPinia } from 'pinia';

import './assets/main.scss';
import { installBuefy } from './utils/buefy.js';

/* import the fontawesome core */
import { library } from '@fortawesome/fontawesome-svg-core';
import { FontAwesomeIcon } from '@fortawesome/vue-fontawesome';

//import all the icons we use.
// just adding 'fas' to the library adds almost a megabyte to the page bundle
// prettier-ignore
import {
    faTrash, faDownload, faEdit, faUpload, faPlus, faWindowClose, faFileDownload, faInfoCircle, faHeartbeat,
    faKey, faQuestionCircle, faFire, faChevronDown, faChevronUp, faArrowUp, faSearch, faExclamationCircle,
    faTag, faBolt, faDesktop, faFileAlt, faNetworkWired, faPlay, faBars, faExclamationTriangle, faCircleNodes, faStop,
    faPlayCircle, faPause, faDatabase, faSave, faCamera, faHistory, faSkullCrossbones, faUndoAlt, 
    faSyncAlt, faPowerOff, faPencil, faArrowRight, faArrowLeft, faCompactDisc, faCheckCircle, faHdd, faMinus, faTerminal,
    faPaintbrush, faTv, faCircle, faCaretDown, faTimesCircle, faAngleLeft, faAngleRight, faCopy,
    faTableColumns, faArrowPointer, faBroom, faEraser, faBook,
    faServer, faClock, faHourglassHalf, faMicrochip, faMemory, faPlug, faUpRightFromSquare, faBan,
    faExpand, faImage, faFileCode, faShareNodes, faListCheck, faRoute,
    faClipboard, faCube, faFolder, faLayerGroup, faFlag, faChartPie, faTableList
} from '@fortawesome/free-solid-svg-icons'

// prettier-ignore
library.add(
    faTrash, faDownload, faEdit, faUpload, faPlus, faWindowClose, faFileDownload, faInfoCircle, faHeartbeat,
    faKey, faQuestionCircle, faFire, faChevronDown, faChevronUp, faArrowUp, faSearch, faExclamationCircle,
    faTag, faBolt, faDesktop, faFileAlt, faNetworkWired, faPlay, faBars, faExclamationTriangle, faCircleNodes, faStop,
    faPlayCircle, faPause, faDatabase, faSave, faCamera, faHistory, faSkullCrossbones, faUndoAlt, 
    faSyncAlt, faPowerOff, faPencil, faArrowRight, faArrowLeft, faCompactDisc, faCheckCircle, faHdd, faMinus, faTerminal,
    faPaintbrush, faTv, faCircle, faCaretDown, faTimesCircle, faAngleLeft, faAngleRight, faCopy,
    faTableColumns, faArrowPointer, faBroom, faEraser, faBook,
    faServer, faClock, faHourglassHalf, faMicrochip, faMemory, faPlug, faUpRightFromSquare, faBan,
    faExpand, faImage, faFileCode, faShareNodes, faListCheck, faRoute,
    faClipboard, faCube, faFolder, faLayerGroup, faFlag, faChartPie, faTableList
)

// icons of our own (see utils/icons.js)
import { faLinux, faSharkFin, faWindows } from './utils/icons.js';
library.add(faLinux, faSharkFin, faWindows);

import App from './App.vue';
import router from './router.js';
import { lazyRouteLoaders, schedulePrefetch } from './utils/prefetch.js';

const app = createApp(App);

app.component('font-awesome-icon', FontAwesomeIcon);

const pinia = createPinia();
app.use(pinia);
app.use(router);
installBuefy(app);

app.mount('#app');

router.isReady().then(() => {
  schedulePrefetch([
    ...lazyRouteLoaders(router.getRoutes()),
    // loaded by pages rather than routes
    () => import('@/components/configs/ConfigsEditor.vue'),
    () => import('@/views/experiment/RunningExperiment.vue'),
    () => import('@/views/experiment/StoppedExperiment.vue'),
  ]);
});
