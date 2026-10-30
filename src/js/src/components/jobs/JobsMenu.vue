<!--
The header's background jobs menu: a button counting the running jobs that
opens the recent jobs. It is a disclosure, not an ARIA menu or Buefy dropdown,
so the panel's buttons stay exposed to assistive technology.
-->
<template>
  <div
    ref="root"
    class="dropdown is-right jobs-menu"
    :class="{ 'is-active': open }"
    @keydown.esc="close(true)"
    @focusout="closeOnFocusOut">
    <div class="dropdown-trigger">
      <button
        ref="trigger"
        type="button"
        class="button is-small jobs-trigger"
        :class="toneClass"
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
        <b-icon
          v-if="tone"
          :icon="tone === 'failed' ? 'exclamation-circle' : 'check-circle'"
          size="is-small"
          class="jobs-unviewed-icon"
          aria-hidden="true"></b-icon>
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
  import {
    unviewedCounts,
    unviewedText,
    unviewedTone,
    useJobStore,
  } from '@/utils/jobs.js';
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
      counts() {
        return unviewedCounts(this.jobs.unviewed);
      },
      tone() {
        return unviewedTone(this.counts);
      },
      toneClass() {
        return this.tone ? `is-unviewed-${this.tone}` : 'is-dark';
      },
      triggerLabel() {
        const running = `${plural(this.jobs.active.length, 'job')} running`;
        const unviewed = unviewedText(this.counts);
        return `Background jobs (${unviewed ? `${running}; ${unviewed}` : running})`;
      },
      // the IDs of the listed jobs not yet shown, which change as jobs end
      unviewedIds() {
        return this.jobs.unviewed.map((job) => job.id).join(' ');
      },
    },
    watch: {
      // a finished job listed while the panel is open has been seen
      open: 'markListedViewed',
      unviewedIds: 'markListedViewed',
    },
    mounted() {
      document.addEventListener('click', this.closeOutside, true);
    },
    beforeUnmount() {
      document.removeEventListener('click', this.closeOutside, true);
    },
    methods: {
      markListedViewed() {
        if (this.open && this.jobs.unviewed.length) {
          this.jobs.markViewed(this.jobs.unviewed);
        }
      },
      // Closes the panel, and with refocus puts focus back on the button
      // that opened it, as Escape does.
      close(refocus) {
        if (!this.open) return;
        this.open = false;
        if (refocus) this.$refs.trigger?.focus();
      },
      // Tabbing out of the menu closes it, as a click outside does.
      closeOnFocusOut(event) {
        const to = event.relatedTarget;
        if (to && !this.$refs.root?.contains(to)) this.close(false);
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

  /* Finished jobs not yet seen: white text is 5.3:1 on this green and 7.1:1
     on the red; the icon and the button's name say the same in words. */
  .jobs-trigger.is-unviewed-succeeded {
    background-color: #1e7b4b;
    border-color: transparent;
    color: white;
  }

  .jobs-trigger.is-unviewed-succeeded:hover {
    background-color: #186a40;
  }

  .jobs-trigger.is-unviewed-failed {
    background-color: #a91f3d;
    border-color: transparent;
    color: white;
  }

  .jobs-trigger.is-unviewed-failed:hover {
    background-color: #8f1a34;
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
