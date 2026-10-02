<template>
  <div class="content">
    <b-field grouped position="is-right" style="margin: 12px 0px">
      <b-field expanded style="display: flex; align-items: center">
        Logs shown: {{ filteredLogs.length }}
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
      <b-field>
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
        <template v-slot="{ item, index }">
          <div class="columns row">
            <div class="log-column level-column">
              <b-tooltip
                :label="item.level"
                type="is-light"
                :position="index < 2 ? 'is-right' : 'is-top'">
                <b-icon
                  :icon="getIconForLevel(item.level)[0]"
                  size="is-small"
                  :type="getIconForLevel(item.level)[1]"
                  style="vertical-align: middle" />
              </b-tooltip>
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

  import axiosInstance from '@/utils/axios.js';
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
  const KNOWN_TYPES = [
    // keep in sync with plog/package.go
    'SECURITY',
    'SOH',
    'SCORCH',
    'PHENIX-APP',
    'ACTION',
    'HTTP',
    'MINIMEGA',
    'SYSTEM',
  ];

  export default {
    components: {
      BDatetimepicker,
      RecycleScroller,
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
        fetch: async (signal) => {
          if (this.isDefaultView()) {
            return pageFetchers.logs(signal);
          }

          // a refresh of a relative range ("Last 1 Hour") moves it to now
          if (this.endNow && this.dateModes[this.dateFilter] !== null) {
            this.startDate = new Date(
              Date.now() - this.dateModes[this.dateFilter] * 1000,
            );
          }
          const query =
            `logs?start=${this.startDate.toISOString()}` +
            (this.endNow ? '' : `&end=${this.endDate.toISOString()}`);
          const resp = await axiosInstance.get(query, { signal });
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

      addWsHandler(this.handleWs);
      // the server sends log lines only to clients that ask for them, and
      // forgets who asked when the websocket reconnects
      this.subscribeLogs('subscribe');
      this.offWsReconnect = onWsReconnect(() =>
        this.subscribeLogs('subscribe'),
      );
    },

    beforeUnmount() {
      this.offWsReconnect();
      this.subscribeLogs('unsubscribe');
      removeWsHandler(this.handleWs);
      this.applySearch.cancel();
      this.loader.stop();
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
      getIconForLevel(level) {
        switch (level) {
          case 'ERROR':
            return ['xmark-circle', 'is-danger'];
          case 'WARN':
            return ['exclamation-circle', 'is-warning'];
          case 'INFO':
            return ['info-circle', ''];
          default:
            return ['question-circle', 'is-light'];
        }
      },
    },

    data() {
      return {
        logs: markRaw([]), // the loaded logs; filtered in computed
        logsVersion: 0, // bumped on push since `logs` itself is not deeply reactive
        suppressWatch: false, // if true, watches won't trigger call
        startDate: new Date(),
        endDate: new Date(),
        endNow: true, // if true, ignore `endDate` and also append streaming logs
        dateFilter: DEFAULT_DATE_FILTER, // dropdown selection. A key of `dateModes`
        levelFilter: 'INFO',
        typeFilter: [],
        searchInput: '', // bound to the search box; applied to searchFilter debounced
        searchFilter: '',
        loaded: false, // false until the first logs arrive
      };
    },
  };
</script>

<style scoped>
  .b-table {
    .table {
      td {
        vertical-align: middle;
      }
    }
  }

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

  .truncate {
    overflow: hidden;
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

  .level-column {
    width: 64px;
    text-align: center;
    justify-content: center;
  }

  .ts-column {
    width: 200px;
  }

  .type-column {
    width: 96px;
    text-align: center;
    justify-content: center;
  }
</style>
