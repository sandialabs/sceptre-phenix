<template>
  <section class="apps-box" aria-labelledby="apps-box-title">
    <header class="apps-box-head">
      <h2 id="apps-box-title" class="apps-box-title">
        <b-icon icon="bolt" size="is-small" />
        Apps
        <span class="tag is-rounded apps-box-count">{{ apps.length }}</span>
      </h2>
      <b-tooltip :label="triggerTooltip" type="is-dark" position="is-left">
        <b-button
          class="apps-box-trigger"
          type="is-success"
          size="is-small"
          icon-left="play"
          aria-label="Trigger apps"
          :disabled="!!triggerBlocked"
          @click="$emit('trigger')"
          >Trigger</b-button
        >
      </b-tooltip>
    </header>
    <ul class="apps-grid" :style="{ '--apps-columns': columns }">
      <li
        v-for="tile in tiles"
        :key="tile.app"
        :class="['app-tile', `is-${tile.state}`]">
        <button
          v-if="tile.triggerable && !triggerBlocked"
          type="button"
          class="app-tile-body is-clickable"
          @click="$emit('trigger', tile.app)">
          <span class="app-tile-name"
            ><span class="is-sr-only">Trigger </span>{{ tile.app }}</span
          >
          <span class="is-sr-only">, </span>
          <span class="app-tile-state">
            <b-icon :icon="tile.icon" size="is-small" />{{ tile.text }}
          </span>
        </button>
        <div v-else class="app-tile-body">
          <span class="app-tile-name">{{ tile.app }}</span>
          <span class="is-sr-only">, </span>
          <span class="app-tile-state">
            <b-icon :icon="tile.icon" size="is-small" />{{ tile.text }}
          </span>
        </div>
      </li>
    </ul>
  </section>
</template>

<script>
  import { roleAllowed } from '@/utils/rbac.js';
  import {
    APP_STATES,
    DEFAULT_APPS,
    appTileState,
    balancedColumns,
  } from './experimentHeader.js';

  // the most app tiles to a row
  const MAX_COLUMNS = 4;

  // The experiment's apps as an even grid of tiles saying what each was last
  // heard doing; a user app's tile or Trigger opens the trigger dialog.
  export default {
    name: 'ExperimentAppsBox',
    props: {
      experiment: { type: String, default: '' },
      apps: { type: Array, default: () => [] },
      // the state the page last heard of for each app, by name
      states: { type: Object, default: () => ({}) },
    },
    // (app?): open the trigger dialog, with app picked
    emits: ['trigger'],
    computed: {
      triggerable() {
        return this.apps.filter((app) => !DEFAULT_APPS.includes(app));
      },
      // why the apps cannot be triggered, or null
      triggerBlocked() {
        if (!roleAllowed('experiments/trigger', 'create', this.experiment)) {
          return 'Cannot trigger apps: you do not have permission';
        }
        if (this.triggerable.length === 0) {
          return 'Cannot trigger apps: this experiment has only built-in apps';
        }
        return null;
      },
      triggerTooltip() {
        return this.triggerBlocked ?? 'pick apps to trigger';
      },
      columns() {
        return balancedColumns(this.apps.length, MAX_COLUMNS);
      },
      tiles() {
        return this.apps.map((app) => {
          const state = appTileState(app, this.states);
          return {
            app,
            state,
            ...APP_STATES[state],
            triggerable: !DEFAULT_APPS.includes(app),
          };
        });
      },
    },
  };
</script>

<style scoped>
  .apps-box {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    background-color: #3d3d3d;
    border: 1px solid #686868;
    border-radius: 8px;
    padding: 0.6rem 0.75rem 0.75rem;
  }

  /* as tall as the summary's head beside it, so their tile rows line up */
  .apps-box-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    min-height: 2.7rem;
  }

  .content h2.apps-box-title {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin: 0;
    font-size: 0.875rem;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: white;
  }

  .apps-box-count {
    letter-spacing: normal;
  }

  /* rows of --apps-columns tiles; a shorter last row's tiles widen to fill
     it, and the rows grow to the box's height */
  .content ul.apps-grid {
    flex: 1;
    display: flex;
    flex-wrap: wrap;
    align-content: stretch;
    gap: 0.5rem;
    columns: auto;
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .content .apps-grid li + li {
    margin-top: 0;
  }

  .app-tile {
    display: flex;
    flex: 1 1
      calc((100% - (var(--apps-columns) - 1) * 0.5rem) / var(--apps-columns));
    min-width: 0;
  }

  .app-tile-body {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    justify-content: center;
    gap: 0.15rem;
    width: 100%;
    padding: 0.4rem 0.6rem;
    background-color: #484848;
    border: 1px solid #686868;
    border-left: 4px solid var(--app-accent);
    border-radius: 6px;
    color: whitesmoke;
    font: inherit;
    text-align: left;
  }

  button.app-tile-body:hover {
    background-color: #5a5a5a;
  }

  .app-tile-name {
    max-width: 100%;
    font-weight: 700;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .app-tile-state {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.75rem;
  }

  .app-tile-state :deep(.icon) {
    color: var(--app-accent);
  }

  /* accents have 3:1 or more against the tile */
  .app-tile.is-builtin {
    --app-accent: #b1b1b1;
  }

  .app-tile.is-user {
    --app-accent: #62c0d7;
  }

  .app-tile.is-running {
    --app-accent: #ffd166;
  }

  .app-tile.is-succeeded {
    --app-accent: #48c78e;
  }

  .app-tile.is-failed {
    --app-accent: #ff7b95;
  }

  .app-tile.is-canceled {
    --app-accent: #ff9f5a;
  }
</style>
