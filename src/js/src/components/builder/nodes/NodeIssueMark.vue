<!--
  The checks' mark on a node's corner: a cross for errors, a triangle for
  warnings only, so shape tells them apart as well as color.
-->
<template>
  <span
    class="builder-node__issue"
    :data-level="issue.level"
    data-testid="node-issue"
    aria-hidden="true">
    <builder-icon
      :name="issue.level === 'error' ? 'close' : 'warning'"
      :size="12" />
  </span>
  <span :id="nodeIssueId(nodeId)" class="builder-node__issue-text" hidden>{{
    issue.text
  }}</span>
</template>

<script setup>
  import BuilderIcon from '../BuilderIcon.vue';

  import { nodeIssueId } from '@/builder/adapters/vueflow.js';

  defineProps({
    nodeId: { type: String, required: true },
    issue: { type: Object, required: true },
  });
</script>
