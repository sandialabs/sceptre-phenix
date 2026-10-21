const activeStates = new Set([
  'starting',
  'running',
  'waiting',
  'canceling',
  'finalizing',
]);

export function executionActive(execution) {
  return activeStates.has(execution?.state);
}

export function mergeScorchRuns(snapshot, previous = []) {
  return (snapshot.pipelines ?? []).map((pipeline, id) => {
    const execution = snapshot.executions?.[id];
    const old = previous[id];
    const same = old?.execution?.executionID === execution?.executionID;
    return {
      name: pipeline.name,
      execution,
      running: executionActive(execution),
      loop: same ? (old?.loop ?? 0) : 0,
      nodes: same && old?.loop > 0 ? old.nodes : pipeline.pipeline,
      pending: old?.pending ?? false,
    };
  });
}

export function terminalKey(terminal) {
  return `${terminal.exp}/${terminal.run}/${terminal.executionID}/${terminal.loop}/${terminal.stage}/${terminal.name}`;
}
