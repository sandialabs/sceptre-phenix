<!--
  A list of checks, grouped by severity: the errors, then the warnings,
  each group under a heading that counts it. The Publish dialog (the checks
  before publishing and what the server refused) and the Checks dialog show
  it.

  Each issue says its severity in words as well as by its icon's shape and
  color, its message, the element it is about and the server's code for it,
  when it has one. An issue the diagram has an element for has a Go to
  button, named by the element and the message, which asks the list's owner
  to go there (see goToIssue in store.js).
-->
<template>
  <div class="builder-issues builder-issue-groups" :data-testid="testid">
    <section
      v-for="group in shown"
      :key="group.severity"
      class="builder-issue-group"
      :data-severity="group.severity"
      :aria-labelledby="`${idPrefix}-${group.severity}`"
      :data-testid="`${testid}-${group.severity}`">
      <component
        :is="`h${headingLevel}`"
        :id="`${idPrefix}-${group.severity}`"
        class="builder-issue-group__heading">
        <builder-icon :name="ICONS[group.severity]" :size="14" />
        {{ severityHeading(group, { blocking }) }}
      </component>
      <ul class="builder-issue-list">
        <li
          v-for="(issue, index) in group.issues"
          :key="`${issue.code || ''}-${issue.path || ''}-${index}`"
          class="builder-issue"
          :data-level="issue.severity"
          data-testid="issue">
          <div class="builder-issue__body">
            <p class="builder-issue__message" data-testid="issue-message">
              <strong>{{ LABELS[issue.severity] }}:</strong>
              {{ issue.text }}
            </p>
            <p v-if="issue.element || issue.code" class="builder-issue__meta">
              <span v-if="issue.element" data-testid="issue-element">{{
                issue.element
              }}</span>
              <code
                v-if="issue.code"
                class="builder-issue__code"
                data-testid="issue-code"
                >{{ issue.code }}</code
              >
            </p>
          </div>
          <button
            v-if="canGoTo && issue.target"
            type="button"
            class="builder-button builder-issue__go"
            :aria-label="goToName(issue)"
            data-testid="issue-go-to"
            @click="$emit('go', issue)">
            Go to
          </button>
        </li>
      </ul>
    </section>
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import BuilderIcon from './BuilderIcon.vue';

  import { goToName, severityHeading } from '@/builder/issues.js';

  const props = defineProps({
    // The groups bySeverity gives, of issues issueEntries gives.
    groups: { type: Array, required: true },
    // Whether the errors stop the diagram from being published, which
    // their heading then says.
    blocking: { type: Boolean, default: false },
    // The level of the groups' headings, under the heading of what holds
    // the list.
    headingLevel: { type: Number, default: 3 },
    // Starts the ids of the groups' headings, so two lists on one page do
    // not share them.
    idPrefix: { type: String, required: true },
    testid: { type: String, default: 'issues' },
    // Whether Go to is offered.
    canGoTo: { type: Boolean, default: true },
  });

  defineEmits(['go']);

  // Told apart by shape as well as color, and by the words.
  const ICONS = { error: 'close', warning: 'warning' };
  const LABELS = { error: 'Error', warning: 'Warning' };

  const shown = computed(() =>
    props.groups.filter((group) => group.issues.length > 0),
  );
</script>

<style scoped>
  /* The global .builder-issues list styles color a whole item and indent
     it; here only the icon and the severity word take the color. */
  .builder-issue-groups.builder-issues {
    padding-left: 0;
    font-size: 0.85rem;
  }

  .builder-issue-group + .builder-issue-group {
    margin-top: 0.6rem;
  }

  .builder-issue-group__heading {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    margin: 0 0 0.3rem;
    font-weight: 700;
    font-size: 0.85rem;
  }

  .builder-issue-group[data-severity='error'] .builder-issue-group__heading,
  .builder-issue-groups li.builder-issue[data-level='error'] strong {
    color: var(--bx-danger);
  }

  .builder-issue-group[data-severity='warning'] .builder-issue-group__heading,
  .builder-issue-groups li.builder-issue[data-level='warning'] strong {
    color: var(--bx-warning);
  }

  .builder-issue-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .builder-issue-groups li.builder-issue[data-level] {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    padding: 0.35rem 0.5rem;
    border: 1px solid var(--bx-border);
    border-left-width: 3px;
    border-radius: var(--bx-radius);
    background: var(--bx-surface);
    color: var(--bx-text);
    overflow-wrap: anywhere;
  }

  .builder-issue-groups li.builder-issue[data-level='error'] {
    border-left-color: var(--bx-danger);
  }

  .builder-issue-groups li.builder-issue[data-level='warning'] {
    border-left-color: var(--bx-warning);
  }

  .builder-issue + .builder-issue {
    margin-top: 0.3rem;
  }

  .builder-issue__body {
    flex: 1 1 auto;
    min-width: 0;
  }

  .builder-issue__message,
  .builder-issue__meta {
    margin: 0;
  }

  .builder-issue__meta {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem 0.5rem;
    margin-top: 0.15rem;
    font-size: 0.78rem;
    color: var(--bx-text-muted);
  }

  .builder-issue__code {
    padding: 0 0.3rem;
    border: 1px solid var(--bx-border);
    border-radius: var(--bx-radius);
    background: transparent;
    color: var(--bx-text-muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 0.75rem;
  }

  .builder-issue__go {
    flex: none;
    align-self: center;
  }
</style>
