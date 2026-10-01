// Network cards: no library, two deterministic passes over each scope (see
// common.js).
//
// 1. A card per network: its devices in a column, grouped by name prefix
//    with a wider gap between prefix groups, devices on several networks
//    first, and its switch at the card's head, right of the column and
//    centred on it, where every connection enters it. A device whose line
//    leaves the switch instead goes in a column right of the switch. More
//    devices than fit one column make staggered columns, so a line from a
//    far column runs through the gaps of the near one.
// 2. The cards on a grid. A device on several networks goes with the
//    smallest, so a card's outside connections point at "parent" networks.
//    Cards are ranked along those connections (children left of parents),
//    ranks become grid columns, a rank taller than the target height wraps
//    into more columns, and the height is chosen for a 16:10 diagram.

import { collator, layoutScopes, orderMembers, packRanks } from './common.js';

const GAP_IN_GROUP = 16;
const GAP_BETWEEN_GROUPS = 48;
const COLUMN_GAP = 48;
const MAX_COLUMN = 8;
// From the devices to the switch. A line bends halfway along, so a line
// from a far column needs the switch far enough away that its bend clears
// the near column.
const TO_SWITCH = 80;
const BEND_CLEARANCE = 48;
const CARD_GAP_X = 96;
const CARD_GAP_Y = 64;
const ASPECT = 1.6;

// Members in columns. On the left of the switch the nearest column is the
// last, and members line up on their right sides; on the right the nearest
// is the first, and they line up on their left sides. `span` is how far
// the farthest column is from the nearest.
function columns(members, side) {
  const at = new Map();

  if (!members.length) {
    return { at, width: 0, height: 0, span: 0 };
  }

  const count = Math.ceil(members.length / MAX_COLUMN);

  if (count === 1) {
    const width = Math.max(...members.map((item) => item.width));
    let y = 0;

    members.forEach((item, index) => {
      const before = members[index - 1];

      if (before) {
        const regroup =
          item.prefix !== before.prefix || item.multi !== before.multi;

        y += before.height + (regroup ? GAP_BETWEEN_GROUPS : GAP_IN_GROUP);
      }
      at.set(item.id, { x: side === 'left' ? width - item.width : 0, y });
    });

    return {
      at,
      width,
      height: y + members[members.length - 1].height,
      span: 0,
    };
  }

  // Column c counts from the switch outwards; every other one is offset by
  // half a row, so lines from farther columns pass between boxes.
  const perColumn = Math.ceil(members.length / count);
  const pitch =
    Math.max(...members.map((item) => item.height)) + 2 * GAP_IN_GROUP;
  const lists = Array.from({ length: count }, (_, c) =>
    members.slice(c * perColumn, (c + 1) * perColumn),
  );
  const widths = lists.map((list) =>
    Math.max(0, ...list.map((item) => item.width)),
  );
  const outwards = side === 'left' ? [...lists].reverse() : lists;
  const outwardWidths = side === 'left' ? [...widths].reverse() : widths;
  let x = 0;
  let height = 0;
  const starts = [];

  outwards.forEach((_, c) => {
    starts.push(x);
    x += outwardWidths[c] + COLUMN_GAP;
  });

  const width = x - COLUMN_GAP;

  outwards.forEach((list, c) => {
    list.forEach((item, row) => {
      const y = row * pitch + (c % 2) * (pitch / 2);

      // starts[c] is measured from the switch's side.
      at.set(item.id, {
        x: side === 'left' ? width - starts[c] - item.width : starts[c],
        y,
      });
      height = Math.max(height, y + item.height);
    });
  });

  return { at, width, height, span: starts[starts.length - 1] };
}

function buildCard(network, scope) {
  const { items, byPrimary } = scope;
  const switches = items
    .filter((item) => item.kind === 'switch' && item.primary === network)
    .sort((a, b) => collator.compare(a.name, b.name) || (a.id < b.id ? -1 : 1));
  const members = byPrimary.get(network) || [];
  // A member whose every line on this network comes from the switch sits
  // right of it.
  const right = (item) => !item.roles.get(network).has('source');
  const ordered = (list) => [
    ...orderMembers(list.filter((item) => item.multi)),
    ...orderMembers(list.filter((item) => !item.multi)),
  ];
  const west = columns(ordered(members.filter((item) => !right(item))), 'left');
  const east = columns(ordered(members.filter(right)), 'right');
  const headHeight =
    switches.reduce((sum, item) => sum + item.height, 0) +
    Math.max(0, switches.length - 1) * GAP_IN_GROUP;
  const headWidth = Math.max(0, ...switches.map((item) => item.width));
  const height = Math.max(west.height, headHeight, east.height);
  const gap = (part) => Math.max(TO_SWITCH, part.span + BEND_CLEARANCE);
  const headX = west.width ? west.width + gap(west) : 0;
  const eastX = switches.length
    ? headX + headWidth + gap(east)
    : west.width && west.width + gap(east);
  const local = new Map();
  const centre = (part) => (height - part.height) / 2;

  west.at.forEach((at, id) =>
    local.set(id, { x: at.x, y: at.y + centre(west) }),
  );
  east.at.forEach((at, id) =>
    local.set(id, { x: eastX + at.x, y: at.y + centre(east) }),
  );

  let y = (height - headHeight) / 2;

  for (const item of switches) {
    local.set(item.id, { x: headX, y });
    y += item.height + GAP_IN_GROUP;
  }

  const width = east.width
    ? eastX + east.width
    : switches.length
      ? headX + headWidth
      : west.width;

  return { network, local, width, height };
}

// Ranks along the connections between cards: an item going with network P
// that also joins Q puts Q's card right of P's when its line leaves toward
// Q, and left of it when the line comes from Q.
function rankCards(scope, cards) {
  const { items, networks } = scope;
  const ids = cards.map((card) => card.network);
  const out = new Map(ids.map((id) => [id, new Set()]));
  const into = new Map(ids.map((id) => [id, new Set()]));
  const byName = (a, b) =>
    collator.compare(networks.get(a).name, networks.get(b).name) ||
    networks.get(a).index - networks.get(b).index;

  for (const item of items) {
    const from = item.primary;

    if (item.kind === 'switch' || !out.has(from)) {
      continue;
    }

    for (const to of item.networks) {
      if (to === from || !out.has(to)) {
        continue;
      }

      if (item.roles.get(to).has('source')) {
        out.get(from).add(to);
      } else {
        out.get(to).add(from);
      }
    }
  }

  // Cycles are broken by a walk in name order, dropping back edges.
  const state = new Map();
  const finished = [];
  const walk = (id) => {
    state.set(id, 'open');
    for (const to of [...out.get(id)].sort(byName)) {
      if (state.get(to) === 'open') {
        out.get(id).delete(to);
      } else if (!state.has(to)) {
        walk(to);
      }
    }
    state.set(id, 'done');
    finished.push(id);
  };

  [...ids].sort(byName).forEach((id) => state.has(id) || walk(id));

  for (const [from, tos] of out) {
    tos.forEach((to) => into.get(to).add(from));
  }

  // The longest path from the sources, then each source pulled up next to
  // its nearest target, so a stub network sits beside the one it joins.
  const rank = new Map();

  for (const id of [...finished].reverse()) {
    rank.set(id, Math.max(0, ...[...into.get(id)].map((p) => rank.get(p) + 1)));
  }
  for (const id of finished) {
    if (into.get(id).size === 0 && out.get(id).size > 0) {
      rank.set(id, Math.min(...[...out.get(id)].map((t) => rank.get(t))) - 1);
    }
  }

  const low = Math.min(...rank.values());

  rank.forEach((value, id) => rank.set(id, value - low));

  return { rank, out, into, byName };
}

function arrangeCards(scope) {
  const byPrimary = new Map();

  for (const item of scope.items) {
    if (item.kind !== 'switch') {
      byPrimary.set(item.primary, [
        ...(byPrimary.get(item.primary) || []),
        item,
      ]);
    }
  }

  const withCards = new Set([
    ...byPrimary.keys(),
    ...scope.items.map((item) => item.primary),
  ]);
  const cards = [...withCards].map((network) =>
    buildCard(network, { ...scope, byPrimary }),
  );
  const { rank, out, into, byName } = rankCards(scope, cards);
  const ranks = [];

  for (const card of cards) {
    const r = rank.get(card.network);

    ranks[r] = [...(ranks[r] || []), card];
  }

  // Within a rank, by the mean slot of the connected cards placed already
  // (a barycentre sweep), then by name.
  const slot = new Map();
  const filled = ranks.filter(Boolean);

  filled.forEach((list, r) => {
    const centre = (card) => {
      const near = [...into.get(card.network), ...out.get(card.network)]
        .filter((id) => slot.has(id))
        .map((id) => slot.get(id));

      return near.length
        ? near.reduce((sum, value) => sum + value, 0) / near.length
        : Infinity;
    };
    const keyed = list.map((card) => ({ card, centre: centre(card) }));

    keyed.sort(
      (a, b) =>
        (a.centre === b.centre ? 0 : a.centre < b.centre ? -1 : 1) ||
        byName(a.card.network, b.card.network),
    );
    list.splice(0, list.length, ...keyed.map((entry) => entry.card));
    list.forEach((card, index) => slot.set(card.network, index + r * 0.001));
  });

  // The column height closest to the aspect ratio wins.
  const packed = packRanks(
    filled.map((list) =>
      list.map((card) => ({
        id: card.network,
        width: card.width,
        height: card.height,
      })),
    ),
    { gapX: CARD_GAP_X, gapY: CARD_GAP_Y, aspect: ASPECT },
  );
  const positions = new Map();

  for (const card of cards) {
    const origin = packed.get(card.network);

    card.local.forEach((at, id) =>
      positions.set(id, { x: origin.x + at.x, y: origin.y + at.y }),
    );
  }

  return positions;
}

/**
 * @param {object} doc builder document
 * @returns {Promise<{positions: object, sizes: object}>} see layoutScopes
 */
export function layout(doc) {
  return layoutScopes(doc, arrangeCards);
}
