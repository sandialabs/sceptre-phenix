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
            <b-field
              label="Start Time"
              label-position="on-border"
              style="width: 100%">
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
            <b-field
              label="End Time"
              label-position="on-border"
              style="width: 100%">
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
            :value="t"
            aria-role="listitem"
            >{{ t }}</b-dropdown-item
          >
        </b-dropdown>
      </b-field>
      <b-field>
        <b-input
          style="width: 360px"
          placeholder="Search log messages"
          v-model="searchFilter"
          icon-right="times-circle"
          icon-right-clickable
          @icon-right-click="searchFilter = ''">
        </b-input>
      </b-field>
    </b-field>
    <div style="position: relative; background: #484848">
      <b-loading :is-full-page="false" v-model="isLoading"></b-loading>
      <div class="columns row mb-0 has-text-weight-bold mx-0">
        <div class="log-column level-column">Level</div>
        <div class="log-column ts-column">Timestamp</div>
        <div class="log-column type-column">Type</div>
        <div class="log-column column is-rest">Message</div>
      </div>

      <RecycleScroller
        ref="logScroller"
        :items="filteredLogs"
        :item-size="36"
        key-field="time"
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
  import { RecycleScroller } from 'vue-virtual-scroller';
  import 'vue-virtual-scroller/dist/vue-virtual-scroller.css';

  import axiosInstance from '@/utils/axios.js';
  import { useErrorNotification } from '@/utils/errorNotif';
  import { addWsHandler, removeWsHandler } from '@/utils/websocket';

  const KNOWN_LEVELS = [
    // in-order
    'DEBUG',
    'INFO',
    'WARN',
    'ERROR',
  ];
  const DATE_MODES = {
    // date dropdown options. text => seconds to go back
    'Last 10 Minutes': 10 * 60,
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
      RecycleScroller,
    },

    async created() {
      this.knownLevels = KNOWN_LEVELS;
      this.dateModes = DATE_MODES;
      this.knownTypes = KNOWN_TYPES;

      this.startDate = new Date(
        Date.now() - this.dateModes[this.dateFilter] * 1000,
      );
      this.getLogs();

      addWsHandler(this.handleWs);
    },

    beforeUnmount() {
      removeWsHandler(this.handleWs);
    },

    computed: {
      filteredLogs: function () {
        let logs = this.logs;
        if (logs === null) return [];

        let currentLevel = this.knownLevels.indexOf(this.levelFilter);
        console.log(
          `${new Date().toISOString()} start filter len=${logs.length}`,
        );
        logs = logs.filter((log) => {
          if (this.knownLevels.indexOf(log.level) < currentLevel) {
            return false;
          }
          if (
            this.searchFilter !== '' &&
            !log.msg.toLowerCase().includes(this.searchFilter.toLowerCase())
          ) {
            return false;
          }
          if (
            this.typeFilter.length > 0 &&
            !this.typeFilter.includes(log.type)
          ) {
            return false;
          }

          return true;
        });
        console.log(
          `${new Date().toISOString()} finish filter len=${logs.length}`,
        );
        return logs;
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
      getLogs() {
        this.isLoading = true;
        let query =
          `logs?start=${this.startDate.toISOString()}` +
          (this.endNow ? '' : `&end=${this.endDate.toISOString()}`);
        console.log(`${new Date().toISOString()} get logs ${query}`);
        axiosInstance
          .get(query)
          .then((response) => {
            console.log(`${new Date().toISOString()} got response`);
            const json = response.data;
            console.log(
              `${new Date().toISOString()} got logs and converted to json len=${json.length}`,
            );
            this.logs = json;
            this.$nextTick(() => {
              this.$refs.logScroller.scrollToPosition(Number.MAX_SAFE_INTEGER);
              this.isLoading = false;
            });
          })
          .catch((err) => {
            useErrorNotification(err);
            this.isLoading = false;
          });
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
      handleWs(msg) {
        if (msg.resource.type == 'log' && this.endNow && !this.isLoading) {
          this.logs.push(msg.result);
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
        logs: [], // the loaded logs; filtered in computed
        suppressWatch: false, // if true, watches won't trigger call
        startDate: new Date(),
        endDate: new Date(),
        endNow: true, // if true, ignore `endDate` and also append streaming logs
        dateFilter: 'Last 10 Minutes', // dropdown selection. A key of `dateModes`
        levelFilter: 'INFO',
        typeFilter: [],
        searchFilter: '',
        isLoading: false,
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

  a.dropdown-item {
    color: #383838 !important;
  }

  a.dropdown-item.is-active {
    background-color: #bdbdbd;
  }

  /* weird glitch with buefy where back/forward arrow had static position, but relative attrs */
  .datepicker :deep(span) {
    position: relative;
  }

  .datepicker :deep(.is-selectable) {
    color: #383838 !important;
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
