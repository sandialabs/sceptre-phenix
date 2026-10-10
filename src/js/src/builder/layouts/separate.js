// What the point layouts (force.js, graphviz.js) share: a random source that
// gives the same numbers for the same seed, and the pass that moves boxes
// apart until no box overlaps another (separateBoxes).
//
// A point layout places points, not boxes: a spring model or rings around a
// root. The Builder's nodes are boxes, wider than tall, with notes below
// some of them, so each of these layouts ends with separateBoxes.

// The space separateBoxes keeps between two boxes. Positions then snap to
// the grid (see layoutScopes), which moves each box at most half a grid
// step. So this must be at least a grid step.
export const BOX_GAP = 32;

// The most rounds of pushing pairs apart before the boxes are placed one at
// a time.
const PUSH_ROUNDS = 200;

// How far the last pass moves a box at each try, along its way out.
const STEP = 8;

// The angle between the ways out of boxes on the centre: the golden angle,
// so no two go the same way.
const GOLDEN_ANGLE = Math.PI * (3 - Math.sqrt(5));

// Less overlap than this is none: floating point leaves crumbs.
const EPSILON = 1e-6;

/**
 * A number for a text, the same every time: 32-bit FNV-1a.
 *
 * @param {string} text
 * @returns {number} a whole number from 0 to 2^32 - 1
 */
export function seedOf(text) {
  let hash = 0x811c9dc5;

  for (let at = 0; at < text.length; at += 1) {
    hash ^= text.charCodeAt(at);
    hash = Math.imul(hash, 0x01000193);
  }

  return hash >>> 0;
}

/**
 * A random source that gives the same numbers for the same seed
 * (mulberry32), in place of Math.random.
 *
 * @param {number} seed
 * @returns {function(): number} each call a number from 0 up to 1
 */
export function seededRandom(seed) {
  let state = seed >>> 0;

  return () => {
    state = (state + 0x6d2b79f5) >>> 0;

    let value = state;

    value = Math.imul(value ^ (value >>> 15), value | 1);
    value ^= value + Math.imul(value ^ (value >>> 7), value | 61);

    return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
  };
}

// How far two boxes overlap across and down, gap included. When both are
// above EPSILON, the boxes overlap.
function overlapOf(a, b) {
  return {
    x: (a.width + b.width) / 2 - Math.abs(a.x - b.x),
    y: (a.height + b.height) / 2 - Math.abs(a.y - b.y),
  };
}

const overlapping = (a, b) => {
  const over = overlapOf(a, b);

  return over.x > EPSILON && over.y > EPSILON;
};

// Pushes each overlapping pair apart, the shorter way, half each. It runs
// in rounds until no pair overlaps or no rounds remain. A sweep from left to
// right finds the pairs.
function pushApart(boxes) {
  for (let round = 0; round < PUSH_ROUNDS; round += 1) {
    const sorted = [...boxes].sort(
      (a, b) => a.x - a.width / 2 - (b.x - b.width / 2) || a.index - b.index,
    );
    let moved = false;

    for (let i = 0; i < sorted.length; i += 1) {
      const a = sorted[i];

      for (let j = i + 1; j < sorted.length; j += 1) {
        const b = sorted[j];

        if (b.x - b.width / 2 >= a.x + a.width / 2) {
          break;
        }

        const over = overlapOf(a, b);

        if (over.x <= EPSILON || over.y <= EPSILON) {
          continue;
        }

        // The way to push b from a: away from it, or by their order when
        // they share a centre.
        const first = a.index < b.index ? 1 : -1;

        if (over.x < over.y) {
          const way = Math.sign(b.x - a.x) || first;

          a.x -= (way * over.x) / 2;
          b.x += (way * over.x) / 2;
        } else {
          const way = Math.sign(b.y - a.y) || first;

          a.y -= (way * over.y) / 2;
          b.y += (way * over.y) / 2;
        }
        moved = true;
      }
    }

    if (!moved) {
      return;
    }
  }
}

// Places the boxes one at a time, nearest the centre first. A box that
// overlaps a box placed before moves straight out from the centre until it
// overlaps none. There is space past every placed box, so this always ends.
function placeInTurn(boxes) {
  const centre = {
    x: boxes.reduce((sum, box) => sum + box.x, 0) / boxes.length,
    y: boxes.reduce((sum, box) => sum + box.y, 0) / boxes.length,
  };
  const distance = (box) => Math.hypot(box.x - centre.x, box.y - centre.y);
  const placed = [];

  for (const box of [...boxes].sort(
    (a, b) => distance(a) - distance(b) || a.index - b.index,
  )) {
    if (placed.some((other) => overlapping(box, other))) {
      const far = distance(box);
      const angle = box.index * GOLDEN_ANGLE;
      const way =
        far > EPSILON
          ? { x: (box.x - centre.x) / far, y: (box.y - centre.y) / far }
          : { x: Math.cos(angle), y: Math.sin(angle) };
      const start = { x: box.x, y: box.y };

      for (
        let step = 1;
        placed.some((other) => overlapping(box, other));
        step += 1
      ) {
        box.x = start.x + way.x * STEP * step;
        box.y = start.y + way.y * STEP * step;
      }
    }

    placed.push(box);
  }
}

/**
 * Moves boxes apart until none overlaps another, with `gap` between any
 * two. Each box moves as little as the pass finds. The result is the same
 * for the same boxes in the same order.
 *
 * @param {Array<{id: string, width: number, height: number}>} items
 * @param {Map<string, {x: number, y: number}>} centres each box's centre,
 *   by id
 * @param {number} [gap]
 * @returns {Map<string, {x: number, y: number}>} each box's top-left
 *   corner, by id
 */
export function separateBoxes(items, centres, gap = BOX_GAP) {
  const boxes = items.map((item, index) => ({
    id: item.id,
    index,
    x: centres.get(item.id).x,
    y: centres.get(item.id).y,
    width: item.width + gap,
    height: item.height + gap,
  }));

  if (boxes.length > 1) {
    pushApart(boxes);
    placeInTurn(boxes);
  }

  return new Map(
    boxes.map((box) => [
      box.id,
      {
        x: box.x - (box.width - gap) / 2,
        y: box.y - (box.height - gap) / 2,
      },
    ]),
  );
}
