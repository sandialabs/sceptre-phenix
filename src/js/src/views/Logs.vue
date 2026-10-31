<template>
  <div class="content">
    <b-field
      class="logs-toolbar"
      grouped
      position="is-right"
      style="margin: 12px 0px">
      <b-field expanded style="display: flex; align-items: center">
        <stat-strip label="Logs shown" :stats="shownStats" />
      </b-field>
      <b-field>
        <b-dropdown
          ref="dateDropdown"
          v-model="dateFilter"
          @active-change="dateDropdownChange"
          :close-on-click="false"
          position="is-bottom-left">
          <template #trigger>
            <b-button
              style="min-width: 200px"
              type="is-light"
              icon-right="caret-down">
              {{ dateDropdownLabel }}
            </b-button>
          </template>
          <b-dropdown-item
            v-for="(_, t) in dateModes"
            :key="t"
            :value="t"
            aria-role="listitem"
            @click="dateDropdownClick">
            {{ t }}
          </b-dropdown-item>
          <b-dropdown-item
            v-if="dateModes[dateFilter] === null"
            separator></b-dropdown-item>
          <b-dropdown-item
            v-if="dateModes[dateFilter] === null"
            aria-role="menu-item"
            :focusable="false"
            custom>
            <b-field label="Start Time">
              <b-datetimepicker v-model="startDate" :max-datetime="endDate">
                <template #right>
                  <b-button
                    label="Now"
                    type="is-primary"
                    @click="startDate = new Date()" />
                </template>
              </b-datetimepicker>
            </b-field>
            <b-field class="py-2">
              <b-switch v-model="endNow">End Now</b-switch>
            </b-field>
            <b-field label="End Time">
              <b-datetimepicker
                v-model="endDate"
                :min-datetime="startDate"
                :disabled="endNow">
                <template #right>
                  <b-button
                    label="Now"
                    type="is-primary"
                    @click="endDate = new Date()" />
                </template>
              </b-datetimepicker>
            </b-field>
          </b-dropdown-item>
        </b-dropdown>
      </b-field>
      <b-field>
        <b-dropdown v-model="levelFilter">
          <template #trigger>
            <b-button
              style="width: 120px"
              type="is-light"
              icon-right="caret-down">
              {{ levelFilter == 'ERROR' ? 'ERROR' : levelFilter + '+' }}
            </b-button>
          </template>
          <b-dropdown-item
            v-for="t in knownLevels"
            :key="t"
            :value="t"
            aria-role="listitem"
            >{{ t }}</b-dropdown-item
          >
        </b-dropdown>
      </b-field>
      <b-field>
        <b-dropdown v-model="typeFilter" multiple>
          <template #trigger>
            <b-button
              style="width: 120px"
              type="is-light"
              icon-right="caret-down">
              {{
                typeFilter.length == 0
                  ? 'All Types'
                  : typeFilter.length == 1
                    ? typeFilter[0]
                    : typeFilter[0] + ' +' + (typeFilter.length - 1)
              }}
            </b-button>
          </template>
          <b-dropdown-item
            v-for="t in knownTypes"
            :key="t"
            :value="t"
            aria-role="listitem"
            >{{ t }}</b-dropdown-item
          >
        </b-dropdown>
      </b-field>
      <b-field :addons="false" class="has-addons">
        <b-input
          v-model="searchInput"
          placeholder="Search log messages"
          icon="search"
          aria-label="Search log messages"></b-input>
        <p v-if="searchInput" class="control">
          <button
            class="button input-button"
            aria-label="Clear log search"
            @click="searchInput = ''">
            <b-icon icon="window-close"></b-icon>
          </button>
        </p>
      </b-field>
      <b-field v-if="canDownloadLogs">
        <div class="log-downloads">
          <b-tooltip
            v-for="file in logDownloads"
            :key="file.name"
            :label="file.tooltip"
            type="is-dark"
            position="is-left"
            multilined>
            <a
              v-if="file.href"
              class="button is-light"
              :href="file.href"
              download
              :aria-label="`Download ${file.label}`">
              <b-icon icon="download" size="is-small" />
              <span>{{ file.label }}</span>
            </a>
            <b-button
              v-else
              disabled
              :loading="logFilesLoading"
              :aria-label="`Download ${file.label}`"
              type="is-light"
              icon-left="download">
              {{ file.label }}
            </b-button>
          </b-tooltip>
        </div>
      </b-field>
    </b-field>
    <div style="position: relative; background: #484848">
      <div class="columns row mb-0 has-text-weight-bold mx-0">
        <div class="log-column level-column">Level</div>
        <div class="log-column ts-column">Timestamp</div>
        <div class="log-column type-column">Type</div>
        <div class="log-column column is-rest">Message</div>
      </div>

      <div v-if="!loaded" class="has-text-centered p-4">
        {{ loadingText('logs') }}
      </div>
      <div v-else-if="filteredLogs.length === 0" class="has-text-centered p-4">
        {{
          logs.length
            ? 'No logs match your filters'
            : 'No logs in this time range'
        }}
      </div>
      <RecycleScroller
        ref="logScroller"
        role="region"
        tabindex="0"
        aria-label="Log messages"
        :items="filteredLogs"
        :item-size="36"
        key-field="id"
        style="width: 100%; height: 75vh">
        <template v-slot="{ item }">
          <div class="columns row">
            <div class="log-column level-column">
              <span
                class="tag level-tag"
                :class="levelBadge(item.level).cls"
                :title="item.level">
                {{ levelBadge(item.level).label }}
              </span>
            </div>

            <div class="log-column ts-column">{{ item.timestamp }}</div>

            <div class="log-column type-column">
              <b-tag style="white-space: nowrap; width: 100%">
                {{ item.type }}
              </b-tag>
            </div>

            <div class="log-column msg-column column is-rest">
              <!-- Bug: if at the top of the list, tooltip won't be visible. Changing position to bottom causes overlaps -->
              <b-tooltip
                :triggers="['click']"
                :label="item.msg"
                position="is-top"
                type="is-light"
                multilined>
                <span class="truncate">
                  {{ item.msg }}
                </span>
              </b-tooltip>
            </div>
          </div>
        </template>
      </RecycleScroller>
    </div>
  </div>
</template>

<script>
  import { markRaw } from 'vue';
  import { BDatetimepicker } from 'buefy';
  import { debounce } from 'lodash-es';
  import { RecycleScroller } from 'vue-virtual-scroller';
  import 'vue-virtual-scroller/dist/vue-virtual-scroller.css';

  import * as logsApi from '@/api/logs.js';
  import StatStrip from '@/components/StatStrip.vue';
  import { usePhenixStore } from '@/store.js';
  import { levelBadge } from '@/utils/logLevels.js';
  import { roleAllowed } from '@/utils/rbac.js';
  import {
    addWsHandler,
    onWsReconnect,
    removeWsHandler,
    sendWsMsg,
  } from '@/utils/websocket';
  import { createPageLoader, loadingText } from '@/utils/pageLoader.js';
  import {
    DEFAULT_LOG_WINDOW,
    pageFetchers,
    withLogIds,
  } from '@/utils/pageData.js';

  const KNOWN_LEVELS = [
    // in-order
    'DEBUG',
    'INFO',
    'WARN',
    'ERROR',
  ];
  const DEFAULT_DATE_FILTER = 'Last 10 Minutes';
  const DATE_MODES = {
    // date dropdown options. text => seconds to go back
    'Last 10 Minutes': DEFAULT_LOG_WINDOW,
    'Last 30 Minutes': 30 * 60,
    'Last 1 Hour': 60 * 60,
    'Last 6 Hours': 6 * 60 * 60,
    'Last 1 Day': 24 * 60 * 60,
    'Last 1 Week': 7 * 24 * 60 * 60,
    'Custom Date Range': null,
  };
  // the log files GET /logs/files lists, as the download buttons name them
  const LOG_FILE_LABELS = { phenix: 'phēnix log', minimega: 'minimega log' };
  // how often the page asks again which log files the server can send
  const LOG_FILES_REFRESH_MS = 30 * 1000;
  const KNOWN_TYPES = [
    // keep in sync with util/plog/package.go
    'AUDIT',
    'SECURITY',
    'SOH',
    'SCORCH',
    'PHENIX-APP',
    'ACTION',
    'HTTP',
    'MINIMEGA',
    'SYSTEM',
  ];

  // where GET /logs/files/{name} downloads from; a link sends no auth header,
  // so the session token goes in the query
  function logFileURL(name, token) {
    const query = token ? `?token=${encodeURIComponent(token)}` : '';
    return `${import.meta.env.BASE_URL}api/v1/logs/files/${encodeURIComponent(name)}${query}`;
  }

  export default {
    components: {
      BDatetimepicker,
      RecycleScroller,
      StatStrip,
    },

    async created() {
      this.filterCache = {}; // non-reactive: see filteredLogs
      this.applySearch = debounce((value) => {
        this.searchFilter = value;
      }, 200);
      this.knownLevels = KNOWN_LEVELS;
      this.dateModes = DATE_MODES;
      this.knownTypes = KNOWN_TYPES;

      this.startDate = new Date(
        Date.now() - this.dateModes[this.dateFilter] * 1000,
      );
      this.loader = createPageLoader({
        // only the default view is reopened, so only it is worth keeping;
        // streamed entries are pushed onto the cached array too
        key: () => (this.isDefaultView() ? 'logs' : null),
        fetch: async (signal, meta) => {
          if (this.isDefaultView()) {
            return pageFetchers.logs(signal, meta);
          }

          // a refresh of a relative range ("Last 1 Hour") moves it to now
          if (this.endNow && this.dateModes[this.dateFilter] !== null) {
            this.startDate = new Date(
              Date.now() - this.dateModes[this.dateFilter] * 1000,
            );
          }
          const resp = await logsApi.listLogs(
            this.startDate,
            this.endNow ? null : this.endDate,
            { signal },
          );
          return withLogIds(resp.data ?? []);
        },
        apply: (logs) => {
          this.logs = markRaw(logs);
          this.loaded = true;
          this.$nextTick(() => {
            // the scroller is gone if the user left the page mid-request
            this.$refs.logScroller?.scrollToPosition(Number.MAX_SAFE_INTEGER);
          });
        },
      });
      this.loader.start();
      this.loadLogFiles();

      addWsHandler(this.handleWs);
      // the server sends log lines only to clients that ask for them, and
      // forgets who asked when the websocket reconnects
      this.subscribeLogs('subscribe');
      this.offWsReconnect = onWsReconnect(
        () => {
          this.subscribeLogs('subscribe');
          this.refreshLogFiles();
        },
        { spread: true },
      );
      this.logFilesTimer = setInterval(
        () => this.refreshLogFiles(),
        LOG_FILES_REFRESH_MS,
      );
    },

    beforeUnmount() {
      this.offWsReconnect();
      this.subscribeLogs('unsubscribe');
      removeWsHandler(this.handleWs);
      this.applySearch.cancel();
      this.loader.stop();
      clearInterval(this.logFilesTimer);
      this.logFilesAbort?.abort();
    },

    watch: {
      searchInput(value) {
        this.applySearch(value);
      },
    },

    computed: {
      // Logs can hold a week of entries and stream in continuously, so the
      // array is kept out of Vue's deep reactivity (logsVersion signals a
      // change) and streamed entries are filtered incrementally instead of
      // re-filtering every log on every message.
      filteredLogs: function () {
        this.logsVersion; // dependency: bumped when logs change
        const logs = this.logs;
        const key = [
          this.levelFilter,
          this.searchFilter.toLowerCase(),
          this.typeFilter.join(','),
        ].join('\n');
        const cache = this.filterCache;
        if (cache.logs !== logs || cache.key !== key) {
          cache.logs = logs;
          cache.key = key;
          cache.count = 0;
          cache.result = [];
        }

        const currentLevel = this.knownLevels.indexOf(this.levelFilter);
        const search = this.searchFilter.toLowerCase();
        const types = this.typeFilter;
        const matches = [];
        for (let i = cache.count; i < logs.length; i++) {
          const log = logs[i];
          if (this.knownLevels.indexOf(log.level) < currentLevel) continue;
          if (search !== '' && !log.msg.toLowerCase().includes(search)) {
            continue;
          }
          if (types.length > 0 && !types.includes(log.type)) continue;
          matches.push(log);
        }
        cache.count = logs.length;
        if (matches.length > 0) {
          cache.result = cache.result.concat(matches);
        }
        return cache.result;
      },
      // every loaded log, and those the filters leave out
      totalLogs() {
        this.logsVersion; // dependency: bumped when logs change
        return this.logs.length;
      },
      hiddenByFilters() {
        return this.totalLogs - this.filteredLogs.length;
      },
      // the count left of the filters, with the total when they hide some
      shownStats() {
        const n = (count) => count.toLocaleString('en-US');
        return [
          {
            key: 'shown',
            label: 'Logs shown',
            value: n(this.filteredLogs.length),
            detail: this.hiddenByFilters > 0 ? `of ${n(this.totalLogs)}` : '',
          },
        ];
      },
      canDownloadLogs() {
        return roleAllowed('logs', 'get');
      },
      // each log file's download button: the link to it, or why there is none
      logDownloads() {
        const listed = new Map(this.logFiles.map((f) => [f.name, f]));
        return Object.entries(LOG_FILE_LABELS).map(([name, label]) => {
          const file = listed.get(name);
          let reason = null;
          if (this.logFilesLoading) reason = 'Checking for the log file';
          else if (this.logFilesError !== null)
            reason = `Could not check the log files: ${this.logFilesError}`;
          else if (!file) reason = 'The server does not offer this log file';
          else if (!file.available)
            reason = `Unavailable: ${file.reason || 'the server cannot send it'}`;
          return {
            name,
            label,
            href:
              reason === null
                ? logFileURL(name, usePhenixStore().token)
                : undefined,
            tooltip: reason ?? `Download the ${label} file`,
          };
        });
      },
      dateDropdownLabel: function () {
        if (this.dateModes[this.dateFilter] === null) {
          let dateOpts = {
            month: 'short',
            day: 'numeric',
            hour: 'numeric',
            minute: 'numeric',
            hour12: true,
          };
          var s = this.startDate.toLocaleString(undefined, dateOpts);
          if (this.endNow) {
            s += ' –  Now';
          } else {
            s += ' – ' + this.endDate.toLocaleString(undefined, dateOpts);
          }
          return s;
        }
        return this.dateFilter;
      },
    },

    methods: {
      loadingText,
      levelBadge,

      // asks which log files the server can send, for the download buttons
      async loadLogFiles() {
        this.logFilesAbort?.abort();
        const ctrl = (this.logFilesAbort = new AbortController());
        try {
          const resp = await logsApi.listLogFiles(ctrl.signal);
          if (ctrl.signal.aborted) return;
          this.logFiles = resp.data?.files ?? [];
          this.logFilesError = null;
        } catch (err) {
          if (ctrl.signal.aborted) return;
          this.logFilesError =
            err?.response?.data?.message ?? err?.message ?? 'unknown error';
        }
        this.logFilesLoading = false;
      },
      // a log file can appear or go away while the page is open
      refreshLogFiles() {
        if (this.canDownloadLogs && !document.hidden) this.loadLogFiles();
      },

      isDefaultView() {
        return this.endNow && this.dateFilter === DEFAULT_DATE_FILTER;
      },
      // loads a newly chosen date range, dropping the old range's logs
      getLogs() {
        this.logs = markRaw([]);
        this.loaded = false;
        this.loader.load();
      },
      // triggers getLogs call when date dropdown closes
      dateDropdownChange(n) {
        if (!n) {
          if (this.dateModes[this.dateFilter] !== null) {
            this.startDate = new Date(
              Date.now() - this.dateModes[this.dateFilter] * 1000,
            );
            this.endNow = true;
          }
          this.getLogs();
        }
      },
      // manages closing the date dropdown on click. We want to keep it open if user is clicking on custom dates
      dateDropdownClick() {
        if (this.dateModes[this.dateFilter] === null) {
          // custom range
          return;
        }
        this.$refs.dateDropdown.isActive = false;
      },
      subscribeLogs(action) {
        sendWsMsg({ resource: { type: 'log', name: 'phenix', action } });
      },
      handleWs(msg) {
        if (msg.resource.type == 'log' && this.endNow) {
          this.logs.push(...withLogIds([msg.result]));
          this.logsVersion++;
        }
      },
    },

    data() {
      return {
        logs: markRaw([]), // the loaded logs; filtered in computed
        logsVersion: 0, // bumped on push since `logs` itself is not deeply reactive
        startDate: new Date(),
        endDate: new Date(),
        endNow: true, // if true, ignore `endDate` and also append streaming logs
        dateFilter: DEFAULT_DATE_FILTER, // dropdown selection. A key of `dateModes`
        levelFilter: 'INFO',
        typeFilter: [],
        searchInput: '', // bound to the search box; applied to searchFilter debounced
        searchFilter: '',
        loaded: false, // false until the first logs arrive
        logFiles: [], // GET /logs/files: the log files to download
        logFilesLoading: true,
        logFilesError: null, // why the log files could not be listed
      };
    },
  };
</script>

<style scoped>
  .control :deep(.icon) {
    height: 1.5em !important;
    top: 50%;
    transform: translateY(-50%);
  }

  a.dropdown-item.is-active {
    background-color: #bdbdbd;
  }

  /* weird glitch with buefy where back/forward arrow had static position, but relative attrs */
  .datepicker :deep(span) {
    position: relative;
  }

  .datepicker :deep(.is-selectable) {
    color: white !important;
  }

  .row {
    width: 100%;
    margin: 0;
    height: 36px;
    border-bottom: 1px solid white;
    line-height: 1.2;
  }

  /* breaking anywhere keeps a long token from widening the message column */
  .truncate {
    overflow: hidden;
    overflow-wrap: anywhere;
    text-overflow: ellipsis;
    padding-top: 2px;
    padding-bottom: 2px;
    margin: auto;
    display: -webkit-box !important;
    -webkit-line-clamp: 2; /* number of lines to show */
    line-clamp: 2;
    -webkit-box-orient: vertical;
  }

  :deep(.vue-recycle-scroller__item-view.hover) {
    background: #777777 !important;
  }
  :deep(.msg-column .b-tooltip .tooltip-content) {
    width: 102% !important;
    text-align: left;
    padding-left: 4px;
    padding-right: 4px;
  }

  .log-column {
    display: flex;
    align-items: center;
    font-family: monospace;
    font-size: 0.85em;
  }

  /* fixed columns never shrink, so every row lines up with the header */
  .level-column,
  .ts-column,
  .type-column {
    flex: none;
  }

  .msg-column {
    min-width: 0;
  }

  /* the gap keeps the badge off the timestamp */
  .level-column {
    width: 76px;
    padding: 0 12px 0 4px;
    text-align: center;
    justify-content: center;
  }

  /* each level's badge; text on fill is at least 4.5:1 */
  .level-tag {
    width: 100%;
    font-weight: 700;
    letter-spacing: 0.04em;
    white-space: nowrap;
  }

  .level-tag.level-debug {
    background-color: #2b2b2b;
    color: #e0e0e0;
    box-shadow: inset 0 0 0 1px #8a8a8a;
  }

  .level-tag.level-info {
    background-color: #1f6f8b;
    color: #fff;
  }

  .level-tag.level-warn {
    background-color: #ffe08a;
    color: #363636;
  }

  .level-tag.level-error {
    background-color: #a91f3d;
    color: #fff;
  }

  /* the filters drop below the count when both do not fit on one row */
  .logs-toolbar > :deep(.field-body > .field.is-grouped) {
    flex-wrap: wrap;
    row-gap: 0.75rem;
  }

  .log-downloads {
    display: flex;
    gap: 0.5em;
  }

  .ts-column {
    width: 26ch;
    white-space: nowrap;
  }

  .type-column {
    width: 96px;
    text-align: center;
    justify-content: center;
  }
</style>
