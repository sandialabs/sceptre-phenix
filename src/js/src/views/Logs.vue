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
        <b-dropdown
          aria-role="list"
          position="is-bottom-left"
          class="log-downloads">
          <template #trigger>
            <b-button icon-left="download" icon-right="caret-down"
              >Download</b-button
            >
          </template>
          <b-dropdown-item
            v-for="file in logDownloads"
            :key="file.name"
            aria-role="listitem"
            :has-link="!!file.href"
            :disabled="!file.href">
            <a
              v-if="file.href"
              :href="file.href"
              download
              :title="file.tooltip">
              {{ file.label }}
            </a>
            <template v-else>
              {{ file.label }}
              <span class="log-download-reason">{{ file.tooltip }}</span>
            </template>
          </b-dropdown-item>
        </b-dropdown>
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
              <log-level-tag :level="item.level" />
            </div>

            <div class="log-column ts-column">{{ item.timestamp }}</div>

            <div class="log-column type-column">
              <b-tag style="white-space: nowrap; width: 100%">
                {{ item.type }}
              </b-tag>
            </div>

            <div class="log-column msg-column column is-rest">
              <!-- A button, so the keyboard can open the details too. -->
              <button
                type="button"
                class="log-message"
                aria-haspopup="dialog"
                @click="openDetails(item, $event)">
                <span class="truncate">{{ item.msg }}</span>
              </button>
            </div>
          </div>
        </template>
      </RecycleScroller>
    </div>
    <!-- one dialog for the page, outside the scroller, so no row covers it -->
    <log-details-modal
      :entry="selected"
      :index="selectedIndex"
      :total="filteredLogs.length"
      @close="closeDetails"
      @previous="stepDetails(-1)"
      @next="stepDetails(1)" />
  </div>
</template>

<script>
  import { markRaw } from 'vue';
  import { BDatetimepicker } from 'buefy';
  import { debounce } from 'lodash-es';
  import { RecycleScroller } from 'vue-virtual-scroller';
  import 'vue-virtual-scroller/dist/vue-virtual-scroller.css';

  import * as logsApi from '@/api/logs.js';
  import LogDetailsModal from '@/components/LogDetailsModal.vue';
  import LogLevelTag from '@/components/LogLevelTag.vue';
  import StatStrip from '@/components/StatStrip.vue';
  import { usePhenixStore } from '@/store.js';
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
      LogDetailsModal,
      LogLevelTag,
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
          this.$nextTick(() => this.scrollToNewest());
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
      // the scroller keeps its offset when its list changes; show the
      // newest entries the filters select, as a load does
      filterKey() {
        this.$nextTick(() => this.scrollToNewest());
      },
    },

    computed: {
      // Logs can hold a week of entries and stream in continuously, so the
      // array is kept out of Vue's deep reactivity (logsVersion signals a
      // change) and streamed entries are filtered incrementally instead of
      // re-filtering every log on every message.
      filteredLogs: function () {
        void this.logsVersion; // dependency: bumped when logs change
        const logs = this.logs;
        const key = this.filterKey;
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
      // the level, search and types that select the logs shown
      filterKey() {
        return [
          this.levelFilter,
          this.searchFilter.toLowerCase(),
          this.typeFilter.join(','),
        ].join('\n');
      },
      // where the details dialog's entry is in the filtered list, or -1
      selectedIndex() {
        if (!this.selected) return -1;
        const id = this.selected.id;
        return this.filteredLogs.findLastIndex((log) => log.id === id);
      },
      // every loaded log, and those the filters leave out
      totalLogs() {
        void this.logsVersion; // dependency: bumped when logs change
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
      // the scroller is gone if the user left the page mid-request
      scrollToNewest() {
        this.$refs.logScroller?.scrollToPosition(Number.MAX_SAFE_INTEGER);
      },
      // opens the details dialog on an entry; focus returns to its button
      openDetails(item, event) {
        this.detailsOpener = event.currentTarget;
        this.selected = item;
      },
      closeDetails() {
        this.selected = null;
        const opener = this.detailsOpener;
        this.detailsOpener = null;
        this.$nextTick(() => {
          if (opener?.isConnected) opener.focus();
        });
      },
      // shows the entry before (-1) or after (1) in the filtered list
      stepDetails(step) {
        const next = this.filteredLogs[this.selectedIndex + step];
        if (this.selectedIndex >= 0 && next) this.selected = next;
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
        selected: null, // the entry the details dialog shows
      };
    },
  };
</script>

<style scoped>
  /* the input's own icons only: this moved the clear button's icon up */
  .control > :deep(.icon) {
    height: 1.5em !important;
    top: 50%;
    transform: translateY(-50%);
  }

  /* the clear button: a square as high as the box, its icon in the middle */
  .button.input-button {
    width: 2.5em;
    min-width: 24px;
    min-height: 24px;
    padding: 0;
  }

  .button.input-button .icon:only-child {
    margin: 0;
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

  /* The message button looks like the text it replaces. */
  .log-message {
    display: block;
    width: 100%;
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: start;
    cursor: pointer;
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

  /* White text is 7.2:1 on a hovered row; it was 4.5:1 on #777777. */
  :deep(.vue-recycle-scroller__item-view.hover) {
    background: #575757 !important;
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

  /* the badge fills its column (LogLevelTag.vue colors it) */
  .level-column .level-tag {
    width: 100%;
  }

  /* the filters drop below the count when both do not fit on one row */
  .logs-toolbar > :deep(.field-body > .field.is-grouped) {
    flex-wrap: wrap;
    row-gap: 0.75rem;
  }

  /* why a log file cannot be downloaded, under its name */
  .log-download-reason {
    display: block;
    font-size: 0.85em;
    max-width: 30ch;
    white-space: normal;
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
