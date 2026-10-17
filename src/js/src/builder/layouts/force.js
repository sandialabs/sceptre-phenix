// Force: a spring model from d3-force, over each scope (see common.js).
// Nodes push each other away (many-body, with the Barnes-Hut
// approximation). Connections pull their ends together. Boxes that touch
// push apart (boxCollide: d3's own collide force is for circles). The
// simulation runs a fixed number of steps at once, without animation. It
// starts from fixed positions and uses a random source seeded by the
// scope's node ids, so the same document gives the same layout. A last pass
// removes any overlap that remains (separateBoxes).

import { forceLink, forceManyBody, forceSimulation, forceX, forceY } from 'd3';

import { layoutScopes } from './common.js';
import { BOX_GAP, seededRandom, seedOf, separateBoxes } from './separate.js';

// The steps the simulation runs: d3's default cooling takes about 300 to
// settle.
const TICKS = 300;

// The length a connection pulls towards, centre to centre.
const LINK_DISTANCE = 200;

// How strongly each node pushes the others away, and how far that reaches.
const CHARGE = -2000;
const CHARGE_REACH = 2000;

// The pull of every node towards the centre, which keeps parts that no
// connection joins near each other.
const GRAVITY = 0.02;

// How much of an overlap boxCollide takes back in one step.
const COLLIDE_STRENGTH = 0.7;

/**
 * A d3 force that pushes overlapping boxes apart, the shorter way. d3's
 * forceCollide takes each node as a circle. Nodes carry `width` and
 * `height`, and `gap` is the space to keep between two nodes.
 *
 * @param {number} gap
 * @returns {function} a d3 force
 */
export function boxCollide(gap) {
  let nodes = [];

  function force() {
    const sorted = [...nodes].sort(
      (a, b) => a.x - a.width / 2 - (b.x - b.width / 2) || a.index - b.index,
    );

    for (let i = 0; i < sorted.length; i += 1) {
      const a = sorted[i];
      const right = a.x + (a.width + gap) / 2;

      for (let j = i + 1; j < sorted.length; j += 1) {
        const b = sorted[j];

        if (b.x - (b.width + gap) / 2 >= right) {
          break;
        }

        const overX = (a.width + b.width) / 2 + gap - Math.abs(a.x - b.x);
        const overY = (a.height + b.height) / 2 + gap - Math.abs(a.y - b.y);

        if (overX <= 0 || overY <= 0) {
          continue;
        }

        const first = a.index < b.index ? 1 : -1;

        if (overX < overY) {
          const push =
            ((Math.sign(b.x - a.x) || first) * overX * COLLIDE_STRENGTH) / 2;

          a.vx -= push;
          b.vx += push;
        } else {
          const push =
            ((Math.sign(b.y - a.y) || first) * overY * COLLIDE_STRENGTH) / 2;

          a.vy -= push;
          b.vy += push;
        }
      }
    }
  }

  force.initialize = (list) => {
    nodes = list;
  };

  return force;
}

// Each connection once, whichever way it runs.
function linksOf(edges) {
  const seen = new Map();

  for (const edge of edges) {
    const key = [edge.source, edge.target].sort().join('\n');

    if (!seen.has(key)) {
      seen.set(key, { source: edge.source, target: edge.target });
    }
  }

  return [...seen.values()];
}

function arrangeForce({ items, edges }) {
  const sorted = [...items].sort((a, b) => (a.id < b.id ? -1 : 1));
  const random = seededRandom(seedOf(sorted.map((item) => item.id).join('\n')));
  // Start positions in a square that gives each node room of its own.
  const side = Math.sqrt(sorted.length) * LINK_DISTANCE;
  const nodes = sorted.map((item) => ({
    id: item.id,
    width: item.width,
    height: item.height,
    x: (random() - 0.5) * side,
    y: (random() - 0.5) * side,
  }));
  const simulation = forceSimulation(nodes)
    .stop()
    .randomSource(random)
    .force(
      'link',
      forceLink(linksOf(edges))
        .id((node) => node.id)
        .distance(LINK_DISTANCE),
    )
    .force('charge', forceManyBody().strength(CHARGE).distanceMax(CHARGE_REACH))
    .force('collide', boxCollide(BOX_GAP))
    .force('x', forceX(0).strength(GRAVITY))
    .force('y', forceY(0).strength(GRAVITY));

  simulation.tick(TICKS);

  return separateBoxes(
    sorted,
    new Map(nodes.map((node) => [node.id, { x: node.x, y: node.y }])),
  );
}

/**
 * @param {object} doc builder document
 * @param {object} [options] showNotes, see layoutScopes
 * @returns {Promise<{positions: object, sizes: object}>} see layoutScopes
 */
export function layout(doc, options = {}) {
  return layoutScopes(doc, arrangeForce, options);
}
