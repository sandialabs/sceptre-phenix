// Runs an options-API component's code without mounting it: a plain object
// stands in for the component instance.
import { createApp, ssrContextKey } from 'vue';

// The stand-in has `fields`, bound methods, computed getters (and setters),
// setup() bindings and data(); a field replaces a member of the same name.
export function makeContext(component, fields = {}) {
  const parts = [...(component.mixins ?? []), component];
  const ctx = { ...fields };
  const own = (name) => Object.hasOwn(fields, name);

  for (const part of parts) {
    for (const [name, fn] of Object.entries(part.methods ?? {})) {
      if (!own(name)) ctx[name] = fn.bind(ctx);
    }
    for (const [name, c] of Object.entries(part.computed ?? {})) {
      if (own(name)) continue;
      const get = typeof c == 'function' ? c : c.get;
      Object.defineProperty(ctx, name, {
        configurable: true,
        enumerable: true,
        get: () => get.call(ctx),
        set: c.set ? (value) => c.set.call(ctx, value) : undefined,
      });
    }
  }

  const state = {
    ...runSetup(component, ctx),
    ...Object.assign({}, ...parts.map((part) => part.data?.call(ctx))),
  };
  for (const [name, value] of Object.entries(state)) {
    if (!own(name)) ctx[name] = value;
  }
  return ctx;
}

// The component's setup() bindings, run inside an app that provides the SSR
// context, since server-compiled setup() notes the component there.
export function runSetup(component, props = {}) {
  if (!component.setup) return {};
  const app = createApp({});
  app.provide(ssrContextKey, {});
  return app.runWithContext(() => component.setup(props, {}));
}
