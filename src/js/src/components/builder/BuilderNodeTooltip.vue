<!--
  The info tooltip of the canvas's devices and switches, which
  useFixedTooltip shows and places (see fixedTooltip.js and
  nodes/nodeTooltip.js): a list of labelled rows, each with one or more
  lines, as nodeInfo.js gives them. It is outside the diagram, so it does
  not grow with the zoom and does not appear in an image of the diagram.

  Assistive technology gets the same facts as the node's description, so
  the tooltip is aria-hidden, as the other fixed tooltips are.
-->
<template>
  <div
    v-if="tooltip.tip.value"
    :ref="tooltip.setTipEl"
    class="builder-tooltip builder-tooltip--fixed builder-tooltip--info"
    data-testid="node-tooltip"
    aria-hidden="true"
    :style="{
      top: `${tooltip.tip.value.top}px`,
      left: `${tooltip.tip.value.left}px`,
    }">
    <dl class="builder-info">
      <template v-for="row in tooltip.tip.value.text.rows" :key="row.label">
        <dt class="builder-info__label">{{ row.label }}</dt>
        <dd class="builder-info__value">
          <span
            v-for="(line, index) in row.lines"
            :key="index"
            class="builder-info__line">
            {{ line }}
          </span>
        </dd>
      </template>
    </dl>
  </div>
</template>

<script setup>
  defineProps({
    tooltip: { type: Object, required: true },
  });
</script>
