<!--
The header's background jobs menu: a button counting the running jobs, which
opens the recent jobs with their progress, a link to each one's experiment,
and their cancel, download and dismiss buttons. It is a disclosure, a button
that shows and hides a panel, rather than an ARIA menu: a menu item's
children are presentational, which would hide the panel's buttons and links
from assistive technology. It is not a Buefy dropdown either, which marks
its panel aria-modal although the panel is no dialog.
-->
<template>
  <div
    ref="root"
    class="dropdown is-right jobs-menu"
    :class="{ 'is-active': open }"
    @keydown.esc="close(true)">
    <div class="dropdown-trigger">
      <button
        ref="trigger"
        type="button"
        class="button is-small is-dark jobs-trigger"
        :aria-label="triggerLabel"
        :aria-expanded="open ? 'true' : 'false'"
        aria-controls="jobs-panel"
        :title="triggerLabel"
        @click="open = !open">
        <b-icon icon="list-check" size="is-small"></b-icon>
        <span
          v-if="jobs.active.length"
          class="tag is-info is-rounded jobs-count"
          aria-hidden="true"
          >{{ jobs.active.length }}</span
        >
      </button>
    </div>
    <div class="dropdown-menu">
      <div class="dropdown-content">
        <div
          id="jobs-panel"
          class="dropdown-item jobs-panel"
          role="region"
          aria-labelledby="jobs-heading">
          <p id="jobs-heading" class="jobs-heading">Background jobs</p>
          <p v-if="!jobs.recent.length" class="jobs-empty">
            No background jobs
          </p>
          <ul v-else class="jobs-list">
            <li v-for="job in jobs.recent" :key="job.id" class="jobs-entry">
              <job-progress :job="job" compact></job-progress>
              <router-link
                v-if="job.experiment"
                :to="{ name: 'experiment', params: { id: job.experiment } }"
                class="jobs-experiment"
                @click="close(false)">
                Experiment {{ job.experiment }}
              </router-link>
            </li>
          </ul>
        </div>
      </div>
    </div>
  </div>
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
    data() {
      return { open: false };
    },
    computed: {
      triggerLabel() {
        const running = this.jobs.active.length;
        return `Background jobs (${plural(running, 'job')} running)`;
      },
    },
    mounted() {
      document.addEventListener('click', this.closeOutside, true);
    },
    beforeUnmount() {
      document.removeEventListener('click', this.closeOutside, true);
    },
    methods: {
      // Closes the panel, and with refocus puts focus back on the button
      // that opened it, as Escape does.
      close(refocus) {
        if (!this.open) return;
        this.open = false;
        if (refocus) this.$refs.trigger?.focus();
      },
      // A click anywhere outside the menu closes it.
      closeOutside(event) {
        if (this.open && !this.$refs.root?.contains(event.target)) {
          this.close(false);
        }
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

  /* The panel is $scheme-main (#686868): whitesmoke text is 5.1:1 on it. */
  .jobs-empty {
    color: whitesmoke;
  }

  /* one column, not the two _general.scss gives every list */
  .jobs-list {
    columns: auto;
  }

  .jobs-entry + .jobs-entry {
    border-top: 1px solid #dbdbdb;
    margin-top: 0.5rem;
    padding-top: 0.5rem;
  }

  /* $link is only 2.7:1 on the panel; white (5.6:1), underlined so it
     still reads as a link. */
  .jobs-experiment {
    font-size: 0.85rem;
    color: white;
    text-decoration: underline;
  }
</style>
