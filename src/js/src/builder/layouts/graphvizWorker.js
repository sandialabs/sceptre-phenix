// The Web Worker that runs Graphviz for the Yifan Hu and Radial layouts
// (graphviz.js). It loads Graphviz once and keeps it for the next layout.
// Each message is {id, dot, engine}. The worker answers {id, centres} (see
// renderLayout), or {id, failed, message}. failed is 'load' when Graphviz
// could not load, and 'layout' when Graphviz could not lay the graph out.

import { loadGraphviz, renderLayout } from './graphvizRender.js';

self.addEventListener('message', async ({ data }) => {
  let graphviz;

  try {
    graphviz = await loadGraphviz();
  } catch (error) {
    self.postMessage({
      id: data.id,
      failed: 'load',
      message: String(error?.message || error),
    });

    return;
  }

  try {
    self.postMessage({ id: data.id, centres: renderLayout(graphviz, data) });
  } catch (error) {
    self.postMessage({
      id: data.id,
      failed: 'layout',
      message: String(error?.message || error),
    });
  }
});
