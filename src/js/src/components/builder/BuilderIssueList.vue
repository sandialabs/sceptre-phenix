<!-- A list of checks, errors first. Go to and Fix ask the owner to act (see
     goToIssue in store.js and issueFix in issueFixes.js). -->
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
            <p
              v-if="issue.element || issue.code || issue.fix"
              class="builder-issue__meta">
              <span v-if="issue.element" data-testid="issue-element">{{
                issue.element
              }}</span>
              <code
                v-if="issue.code"
                class="builder-issue__code"
                data-testid="issue-code"
                >{{ issue.code }}</code
              >
              <span v-if="issue.fix" data-testid="issue-fix-hint"
                >Fix: {{ issue.fix.name }}</span
              >
            </p>
          </div>
          <button
            v-if="issue.fix"
            type="button"
            class="builder-button builder-issue__fix"
            :aria-label="`Fix: ${issue.fix.name}`"
            data-testid="issue-fix"
            @click="$emit('fix', issue, offset(group) + index)">
            Fix
          </button>
          <button
            v-if="issue.target"
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
    // The groups from bySeverity, of the issues from issueEntries.
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
  });

  // go (issue), and fix (issue, position): position counts the issues
  // before it, in every group shown.
  defineEmits(['go', 'fix']);

  // The severities differ by shape as well as color, and by the words.
  const ICONS = { error: 'close', warning: 'warning' };
  const LABELS = { error: 'Error', warning: 'Warning' };

  const shown = computed(() =>
    props.groups.filter((group) => group.issues.length > 0),
  );

  // How many issues the groups before `group` list.
  function offset(group) {
    const groups = shown.value;

    return groups
      .slice(0, groups.indexOf(group))
      .reduce((sum, entry) => sum + entry.issues.length, 0);
  }
</script>

<style scoped>
  /* The global .builder-issues list styles color a whole item and indent
     it. Here only the icon and the severity word take the color. */
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

  .builder-issue__fix,
  .builder-issue__go {
    flex: none;
    align-self: center;
  }
</style>
