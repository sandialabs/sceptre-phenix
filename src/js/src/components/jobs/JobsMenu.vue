<!--
The header's background jobs menu: a button counting the running jobs, which
opens the recent jobs with their progress, a link to each one's experiment,
and their cancel, download and dismiss buttons. It is a disclosure, a button
that shows and hides a panel, rather than an ARIA menu: a menu item's
children are presentational, which would hide the panel's buttons and links
from assistive technology.
-->
<template>
  <b-dropdown position="is-bottom-left" :mobile-modal="false" class="jobs-menu">
    <template #trigger="{ active }">
      <button
        type="button"
        class="button is-small is-dark jobs-trigger"
        :aria-label="triggerLabel"
        :aria-expanded="active ? 'true' : 'false'"
        aria-controls="jobs-panel"
        :title="triggerLabel">
        <b-icon icon="list-check" size="is-small"></b-icon>
        <span
          v-if="jobs.active.length"
          class="tag is-info is-rounded jobs-count"
          aria-hidden="true"
          >{{ jobs.active.length }}</span
        >
      </button>
    </template>
    <b-dropdown-item
      custom
      id="jobs-panel"
      role="region"
      aria-labelledby="jobs-heading"
      class="jobs-panel">
      <p id="jobs-heading" class="jobs-heading">Background jobs</p>
      <p v-if="!jobs.recent.length" class="jobs-empty">No background jobs</p>
      <ul v-else class="jobs-list">
        <li v-for="job in jobs.recent" :key="job.id" class="jobs-entry">
          <job-progress :job="job" compact></job-progress>
          <router-link
            v-if="job.experiment"
            :to="{ name: 'experiment', params: { id: job.experiment } }"
            class="jobs-experiment">
            Experiment {{ job.experiment }}
          </router-link>
        </li>
      </ul>
    </b-dropdown-item>
  </b-dropdown>
</template>

<script>
  import JobProgress from '@/components/jobs/JobProgress.vue';
  import { useJobStore } from '@/utils/jobs.js';
  import { plural } from '@/utils/plural.js';

  export default {
    components: { JobProgress },
    setup() {
      return { jobs: useJobStore() };
    },
    computed: {
      triggerLabel() {
        const running = this.jobs.active.length;
        return `Background jobs (${plural(running, 'job')} running)`;
      },
    },
  };
</script>

<style scoped>
  .jobs-trigger {
    gap: 0.25rem;
  }

  .jobs-count {
    height: 1.4em;
    padding: 0 0.5em;
  }

  .jobs-panel {
    min-width: 22rem;
    max-width: 28rem;
    max-height: 70vh;
    overflow-y: auto;
  }

  .jobs-heading {
    font-weight: 600;
    margin-bottom: 0.5rem;
  }

  .jobs-empty {
    color: #4a4a4a;
  }

  .jobs-entry + .jobs-entry {
    border-top: 1px solid #dbdbdb;
    margin-top: 0.5rem;
    padding-top: 0.5rem;
  }

  .jobs-experiment {
    font-size: 0.85rem;
  }
</style>
