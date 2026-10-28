// Unit project only: the Builder store needs the mocks of its setup file.
import { createPinia } from 'pinia';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';

import { useBuilderStore } from '@/builder/store.js';

// Renders a component with a new Builder store that prepare sets up first.
// `capture` maps names to components whose instances come back by that name.
export async function renderWithStore(
  component,
  { props, prepare, capture = {} } = {},
) {
  const pinia = createPinia();
  const app = createSSRApp({ render: () => h(component, props) });
  const instances = Object.fromEntries(
    Object.keys(capture).map((name) => [name, []]),
  );

  app.use(pinia);
  app.mixin({
    created() {
      for (const [name, type] of Object.entries(capture)) {
        if (this.$.type === type) {
          instances[name].push(this.$);
        }
      }
    },
  });

  const store = useBuilderStore(pinia);

  prepare?.(store);

  return { html: await renderToString(app), store, instances };
}
