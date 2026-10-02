// The default State of Health layout: a live d3 force simulation that keeps
// relaxing while nodes are dragged.
import { forceCenter, forceLink, forceManyBody, forceSimulation } from 'd3';

export const kind = 'simulation';

export function createSimulation(nodes, links, { width, height }) {
  return forceSimulation(nodes)
    .force(
      'link',
      forceLink(links).id((d) => d.id),
    )
    .force('charge', forceManyBody())
    .force('center', forceCenter(width / 2, height / 2));
}
