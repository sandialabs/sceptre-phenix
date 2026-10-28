// The drawings of the built-in icons: the elements of a 24 by 24 SVG view
// box, drawn in currentColor with round 1.6 pixel strokes. BuilderIcon.vue
// draws them on the page. A download writes them in files of their own (see
// builtinIconSvg in builtinIcons.js).

// The command key symbol, after Lucide "command" (MIT): the apple icon
// and the chrome icon of the command list.
const COMMAND_KEY =
  '<path d="M9 6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3z"/>';

// Keys of the bounded server icon registry (phenix/types/builder icons.go)
// come first, in the order of ICON_ORDER in iconLabels.js. The remaining
// entries are UI chrome icons. components/builder/ICON_NOTICES.md names the
// source and license of each drawing that is not original.
const PATHS = {
  server:
    '<rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/><circle cx="7" cy="7" r="0.9"/><circle cx="7" cy="17" r="0.9"/>',
  desktop:
    '<rect x="3" y="4" width="18" height="11" rx="1.5"/><path d="M9 19h6M12 15v4"/>',
  // After Lucide "laptop" (ISC).
  laptop:
    '<path d="M5 15.5v-9A1.5 1.5 0 0 1 6.5 5h11A1.5 1.5 0 0 1 19 6.5v9"/><path d="M2.5 19.5h19l-2.5-4H5z"/>',
  // After Lucide "smartphone" (MIT).
  smartphone:
    '<rect x="6.5" y="2.5" width="11" height="19" rx="2"/><path d="M11 18.5h2"/>',
  // After Lucide "printer" (ISC).
  printer:
    '<path d="M6.5 17.5h-2A1.5 1.5 0 0 1 3 16v-5.5A1.5 1.5 0 0 1 4.5 9h15a1.5 1.5 0 0 1 1.5 1.5V16a1.5 1.5 0 0 1-1.5 1.5h-2"/><path d="M6.5 9V3.5h11V9"/><path d="M6.5 14h11v6.5h-11z"/><path d="M17.5 11.75h.01"/>',
  // A clock above a server: a time server.
  ntp: '<circle cx="12" cy="7.5" r="5.5"/><path d="M12 5v2.5l1.8 1.1M12 13v2"/><rect x="3" y="15" width="18" height="5.5" rx="1.5"/><circle cx="6.5" cy="17.75" r=".9"/>',
  // The round router of network diagrams: two arrows in, two arrows out.
  router:
    '<circle cx="12" cy="12" r="9.5"/><path d="M5 12h14M7.5 9.5L5 12l2.5 2.5M16.5 9.5L19 12l-2.5 2.5M12 4.5v5M10 7.5l2 2 2-2M12 19.5v-5M10 16.5l2-2 2 2"/>',
  // A flat box with a row of ports.
  switch:
    '<rect x="2.5" y="8" width="19" height="8" rx="1.5"/><path d="M5.5 11h2.5v2.5H5.5zM10.75 11h2.5v2.5h-2.5zM16 11h2.5v2.5H16z"/>',
  // A brick wall with a flame behind it, after Lucide "brick-wall-fire"
  // (ISC).
  firewall:
    '<path d="M3 12.5h18V21H3z"/><path d="M3 16.75h18M9 12.5v4.25M15 12.5v4.25M6 16.75V21M12 16.75V21M18 16.75V21"/><path d="M8.2 12.5c-1-2.3-.2-4.5 1.6-6 .1 1.5.7 2.4 1.5 2.8-.2-2.7 1.2-5 3.4-6.3-.4 2.4 1.4 3.9 2.1 5.9.5 1.3.4 2.6-.1 3.6"/>',
  // Three hosts on a dashed ring: a virtual LAN. The dashes are a ninth
  // of the ring each, so each host sits in a gap.
  vlan: '<circle cx="12" cy="12" r="7.5" stroke-dasharray="2.618 2.618"/><g fill="currentColor"><circle cx="12" cy="4.5" r="1.4"/><circle cx="18.5" cy="15.75" r="1.4"/><circle cx="5.5" cy="15.75" r="1.4"/></g>',
  // A ribbed shipping container.
  container:
    '<path d="M2.5 8.5l4-3h15v11l-4 3h-15z"/><path d="M2.5 8.5h15v11M17.5 8.5l4-3"/><path d="M5.5 11v6M8.5 11v6M11.5 11v6M14.5 11v6"/>',
  // A monitor drawn in dashes: a device outside the experiment. Each
  // corner of the screen is one dash, so the screen keeps its shape at
  // 12px.
  external:
    '<path d="M3 7V5.5A1.5 1.5 0 0 1 4.5 4H7M9.5 4h5M17 4h2.5A1.5 1.5 0 0 1 21 5.5V7M21 9v2M21 13v1.5A1.5 1.5 0 0 1 19.5 16H17M14.5 16h-5M7 16H4.5A1.5 1.5 0 0 1 3 14.5V13M3 11V9"/><path d="M12 16v4M8 20h8"/>',
  // A controller with terminal rows at the top and bottom, status lights
  // and I/O channels.
  plc: '<rect x="4" y="3.5" width="16" height="17" rx="1.5"/><path d="M4 7h16M4 17h16"/><path d="M6.5 5.25h.01M9 5.25h.01M11.5 5.25h.01M14 5.25h.01M16.5 5.25h.01M6.5 18.75h.01M9 18.75h.01M11.5 18.75h.01M14 18.75h.01M16.5 18.75h.01"/><path d="M11 7v10"/><path d="M7.5 10h.01M7.5 13h.01M14 10h3M14 13h3"/>',
  // An outdoor cabinet with an antenna and radio waves.
  rtu: '<path d="M3 9h13"/><path d="M4.5 9v12h10V9"/><path d="M11.5 13v3"/><path d="M14 9V3.5"/><path d="M16.5 6a3.5 3.5 0 0 0-1.2-3M19 7a6.5 6.5 0 0 0-2.2-5"/><path d="M3 21h13"/>',
  // A rack-mount controller: rack ears, status lights and two ports.
  rtac: '<rect x="2" y="6.5" width="20" height="11" rx="1"/><path d="M5 6.5v11M19 6.5v11"/><path d="M3.5 9h.01M3.5 15h.01M20.5 9h.01M20.5 15h.01"/><path d="M7.5 9.5h.01M7.5 12h.01M7.5 14.5h.01"/><path d="M10 10.5h2.5v3H10zM14 10.5h2.5v3H14z"/>',
  // A protective relay front panel: display, target lights, buttons.
  relay:
    '<rect x="4" y="2.5" width="16" height="19" rx="1.5"/><rect x="7" y="5.5" width="10" height="4.5" rx=".5"/><path d="M7.5 13.5h.01M10.5 13.5h.01M13.5 13.5h.01M16.5 13.5h.01"/><circle cx="9" cy="17.5" r="1"/><circle cx="15" cy="17.5" r="1"/>',
  // A lattice transmission tower: a tapered frame, one crossarm and a
  // cross brace.
  transmission:
    '<path d="M10.5 2.5L7 21.5M13.5 2.5l3.5 19"/><path d="M10.5 2.5h3M3.5 7h17"/><path d="M9.6 12h4.8M9.6 12l7.2 9M14.4 12l-7.2 9"/>',
  // A pole with a crossarm, insulators and braces, after Lucide
  // "utility-pole" (ISC).
  distribution:
    '<path d="M12 2.5v19"/><path d="M3 6.5h18"/><path d="M4.5 6.5V4M8.5 6.5V4M15.5 6.5V4M19.5 6.5V4"/><path d="M8 6.5l4 4 4-4"/>',
  // A tower with radio waves, after Lucide "radio-tower" (ISC).
  'cell-tower':
    '<path d="M8 21l4-11 4 11M9.5 17h5"/><circle cx="12" cy="8" r="1.5"/><path d="M8.5 4.5a5 5 0 0 0 0 7M15.5 4.5a5 5 0 0 1 0 7M5.5 2a9 9 0 0 0 0 12M18.5 2a9 9 0 0 1 0 12"/>',
  // A penguin, simplified from Tux by Larry Ewing.
  linux:
    '<path d="M8 9.5C8 5 9.5 2.5 12 2.5S16 5 16 9.5c1.6 2 3 4.3 3 6.8 0 1.6-.6 2.5-1.6 3H6.6C5.6 18.8 5 17.9 5 16.3c0-2.5 1.4-4.8 3-6.8z"/><path d="M9.3 19.2c-.9-1-1.3-2.6-1.3-4.3 0-2.2 1.8-4.3 4-4.3s4 2.1 4 4.3c0 1.7-.4 3.3-1.3 4.3"/><path d="M10.5 6h.01M13.5 6h.01"/><path d="M10.8 8h2.4L12 9.4z"/><path d="M3.5 21c1.5-1.3 3.8-1.6 5.5-.5M20.5 21c-1.5-1.3-3.8-1.6-5.5-.5"/>',
  // An original four-part pinwheel, not the CentOS logo. It is filled,
  // with a gap at the center: outlines meet there and hide the shape.
  centos:
    '<path fill="currentColor" stroke="none" d="M12.9 11.1V3h8.1zM12.9 12.9H21V21zM11.1 12.9V21H3zM11.1 11.1H3V3z"/>',
  // A plain fedora hat, not the Red Hat logo.
  redhat:
    '<path d="M6 15.5C6 11 7 6 9.5 6c1 0 1.6.8 2.5.8S13.5 6 14.5 6C17 6 18 11 18 15.5"/><path d="M6.2 13.3c3.8 1 7.8 1 11.6 0"/><path d="M2.5 14.5c1.5 2.5 5.5 4 9.5 4s8-1.5 9.5-4"/>',
  // The icons from ubuntu to bsd are original drawings, not logos.
  // A badge with three dots on a ring.
  ubuntu:
    '<circle cx="12" cy="12" r="9.5"/><circle cx="12" cy="12" r="5"/><g fill="currentColor"><circle cx="17" cy="12" r="1.9"/><circle cx="9.5" cy="7.67" r="1.9"/><circle cx="9.5" cy="16.33" r="1.9"/></g>',
  // A plain "f" in a speech bubble.
  fedora:
    '<path d="M5 3h14a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H8l-5 3.5V5a2 2 0 0 1 2-2z"/><path d="M14.5 6.8c-.4-.4-1-.6-1.6-.6-1.3 0-2.1.8-2.1 2.2V15.5M8.5 10h6"/>',
  // A chameleon from the side, with a curled tail.
  opensuse:
    '<path d="M21 11C20.4 8.5 18.8 6.4 16.5 6.1 14.5 5 12 4.9 9.8 5.8 7.5 6.8 5 9 4 11.8 3.2 14.4 3.6 17.4 6 18.3 8 19 9.6 17.6 9.3 15.8 9 14.4 7.6 14 6.6 14.6 5.8 15.1 6 16.3 6.8 16.4"/><path d="M21 11c-.5 1.2-1.7 1.7-3.2 1.5-2.8-.3-5.3.1-7.8 0"/><path d="M16.5 12.5l1 3.5h1.5M10.8 12.5l-.6 3.5h1.5"/><path d="M18 8.8h.01"/>',
  // A mountain peak with a line of snow.
  alpine:
    '<path d="M2.5 19.5L9.5 6l3.5 6.5 3-4 5.5 11z"/><path d="M7.4 10l1.3 1.3 1-1 1 1L11.6 10"/>',
  // The head of a dragon: a horn, a spiked frill and open jaws with
  // teeth.
  kali: '<path d="M5 21.5c.3-2.5.8-4.5 1.5-6.3L4 14l2.4-1.8-2.9-1.4 3-1.3-2.6-2 3.4-.4C6.8 5 5.6 3.4 3.5 2.3c2.6 0 4.7 1.3 6.1 3.4 1.7-.6 3.6-.7 5.4-.2L20.5 8l-.5 2.3-5.5.7 4.8 1.6-.9 1.9-5.2-.4c-1 1.4-1.8 3.4-2.1 7.4"/><path fill="currentColor" stroke="none" d="M16.6 10.6l1.6-.2-.6 1.6zM15.4 12.1l1.5.5-1.2 1z"/><path d="M14.5 7.8h.01"/>',
  // A small horned daemon with a trident.
  bsd: '<circle cx="10" cy="10" r="4.5"/><path d="M6.6 7.2C5.5 6 5.2 4.3 5.8 2.7M13.4 7.2c1.1-1.2 1.4-2.9.8-4.5"/><path d="M8.4 9.5h.01M11.6 9.5h.01"/><path d="M3.5 21.5c0-3.6 2.9-6.3 6.5-6.3s6.5 2.7 6.5 6.3"/><path d="M19.5 21.5v-17M17 3.5v2a2.5 2.5 0 0 0 5 0v-2"/>',
  windows:
    '<path d="M4 6l7-1v6H4z"/><path d="M13 4.7L20 4v7h-7z"/><path d="M4 13h7v6l-7-1z"/><path d="M13 13h7v7l-7-1z"/>',
  // The command key symbol, not the Apple logo.
  apple: COMMAND_KEY,
  // The Android robot by Google, as an outline (CC BY 3.0).
  android:
    '<path d="M6.5 11a5.5 5.5 0 0 1 11 0z"/><path d="M8.5 6.5L7 4M15.5 6.5L17 4"/><path d="M10 8.75h.01M14 8.75h.01"/><path d="M6.5 12.5h11v6a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1z"/><path d="M4 12.5v5M20 12.5v5M9.5 19.5V22M14.5 19.5V22"/>',
  // A window with a title bar and a prompt, after Lucide
  // "square-terminal" (ISC).
  terminal:
    '<rect x="2.5" y="4" width="19" height="16" rx="2"/><path d="M2.5 8h19"/><path d="M6.5 11.5L9 14l-2.5 2.5M11 16.5h5.5"/>',
  // A shield with a filter funnel: a packet filter. Not the pfSense logo.
  pfsense:
    '<path d="M12 2.5l7.5 3v5.5c0 5-3.3 8.5-7.5 10.5C7.8 19.5 4.5 16 4.5 11V5.5z"/><path d="M8 8h8l-3 3.5v4.5l-2-1v-3.5z"/>',
  // A shark fin over a wave and a stream of packets, after Lucide Lab
  // "waves-shark-fin" (ISC). Not the Wireshark logo.
  wireshark:
    '<path d="M17.3 14.8C15.3 11 15.8 6.2 19 3 11.6 3 5.6 8.7 5.1 16"/><path d="M2 15c.6.5 1.2 1 2.5 1 2.5 0 2.5-2 5-2 2.6 0 2.4 2 5 2 2.5 0 2.5-2 5-2 1.3 0 1.9.5 2.5 1"/><path d="M3 20.5h2.5M8 20.5h2.5M13.5 20.5h2.5M18.5 20.5h2.5"/>',
  // An eye on a network line: a network sensor. Not the Zeek logo.
  zeek: '<path d="M2.5 9C5 4.5 8.5 3 12 3s7 1.5 9.5 6c-2.5 4.5-6 6-9.5 6S5 13.5 2.5 9z"/><circle cx="12" cy="9" r="2.5"/><path d="M12 15v4.5M3 19.5h18"/>',
  // A hooded figure: eyes in the shadow of the hood, and drawstrings.
  hacker:
    '<path d="M2.5 21.5c0-3.8 1.3-6.3 2.8-7.4C5 8.5 8 3.4 12 2c4 1.4 7 6.5 6.7 12.1 1.5 1.1 2.8 3.6 2.8 7.4"/><path d="M8.2 14.3c0-3.8 1.6-6.9 3.8-8 2.2 1.1 3.8 4.2 3.8 8 0 1.6-1.7 2.6-3.8 2.6s-3.8-1-3.8-2.6z"/><path fill="currentColor" stroke="none" d="M9.6 11.6l2 .7-.3.9-1.7-.6zM14.4 11.6l-2 .7.3.9 1.7-.6z"/><path d="M10.3 17v3M13.7 17v3"/>',
  // A figure with a wide-brim hat and dark glasses, after Tabler "spy" (MIT).
  // The only drawing with its own color: a near-black hat and glasses with a
  // thin outline, so the dark theme does not show a white hat.
  blackhat:
    '<g class="builder-icon__ink" fill="#111111" stroke-width="0.8"><path d="M7.4 8.6L8.3 4C8.5 3.1 9.1 2.5 10.1 2.5h3.8c1 0 1.6.6 1.8 1.5l.9 4.6z"/><path d="M1.5 9.6c0-.9 4.7-1.5 10.5-1.5s10.5.6 10.5 1.5-4.7 1.5-10.5 1.5S1.5 10.5 1.5 9.6z"/><path d="M5.8 13h5.4v1.4a2 2 0 0 1-2 2H7.8a2 2 0 0 1-2-2zM12.8 13h5.4v1.4a2 2 0 0 1-2 2h-1.4a2 2 0 0 1-2-2z"/></g><path d="M11.2 13.7h1.6"/><path d="M4 21.5l3.5-4.3 2 4.3M20 21.5l-3.5-4.3-2 4.3"/>',
  // A head and shoulders, after Lucide "user" (ISC).
  human:
    '<circle cx="12" cy="7.5" r="4"/><path d="M4.5 21v-1.5a5 5 0 0 1 5-5h5a5 5 0 0 1 5 5V21"/>',
  // A square robot head with one antenna, after Lucide "bot" (ISC).
  robot:
    '<path d="M12 8V4H8"/><rect x="4" y="8" width="16" height="12" rx="2"/><path d="M2 14h2M20 14h2M9 12.5v2M15 12.5v2M10 17h4"/>',
  // A lizard from above: a filled body, four bent legs and a curved tail.
  lizard:
    '<path fill="currentColor" d="M12 1.8c1.2 0 1.9 1.1 1.9 2.5 0 .9-.4 1.6-.9 2.1 1 .6 1.5 2 1.5 4.1 0 2.3-.9 4-2.5 4.4-1.6-.4-2.5-2.1-2.5-4.4 0-2.1.5-3.5 1.5-4.1-.5-.5-.9-1.2-.9-2.1 0-1.4.7-2.5 1.9-2.5z"/><path d="M12 14.5c-.2 3 .6 5.3 2.7 6.3 1.5.7 3 .3 3.8-.8"/><path d="M10.3 8.3L7.3 9 6.3 6M13.7 8.3l3 .7 1-3M10.3 12.3l-3 .4-1 3M13.7 12.3l3 .4 1 3"/>',
  // The head of a fox: pointed ears, cheeks and a snout.
  fox: '<path d="M3 3l5.5 4.5h7L21 3l-1 8.5-8 9.5-8-9.5z"/><path d="M4 11.5l5 2 3 7.5 3-7.5 5-2"/><path d="M9 10.5h.01M15 10.5h.01"/><path d="M11 19h2l-1 1.2z" fill="currentColor"/>',
  // The phenix logo, simplified: two plumes, and a head with a beak, an
  // eye and the line of the head. It is filled: an outline does not show
  // its shape at 16px.
  phenix:
    '<g fill="currentColor" stroke="none" fill-rule="evenodd"><path d="M4.6 11.2L8.7 14 6 10.4 9.3 4.4 11 10.5 8.1 11.7 12.3 14.8 10.3 12 12.2 11.5 15.5 12.5 19.8 16.4 17.6 17.1 18.5 18.6 15 22.4 5 18.4zM12.75 13.9a.85.85 0 1 0 1.7 0a.85.85 0 1 0-1.7 0z"/><path d="M11.7 5.8L17.2 1.5 15.6 11.3 12.4 10.2 12 10z"/></g>',
  // The minimega logo, simplified: an "m" in a square.
  minimega:
    '<rect x="3" y="3" width="18" height="18" rx="1"/><path d="M7 17v-6.5M7 12.5c0-1.4.9-2.3 2.25-2.3s2.25.9 2.25 2.3V17M11.5 12.5c0-1.4.9-2.3 2.25-2.3S16 11.1 16 12.5V17"/>',
  route:
    '<circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="6" r="2.5"/><path d="M8.5 18h5a4 4 0 0 0 4-4V8.5"/>',
  'shield-alt': '<path d="M12 3l7 3v5c0 5-3 8-7 10-4-2-7-5-7-10V6z"/>',
  microchip:
    '<rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4"/>',
  print:
    '<path d="M7 9V4h10v5"/><rect x="4" y="9" width="16" height="7" rx="1.5"/><path d="M7 16h10v4H7z"/>',
  'network-wired':
    '<path d="M12 3l7 4v6l-7 4-7-4V7z"/><path d="M5 9h14M12 3v14"/>',
  'sticky-note':
    '<path d="M5 4h14v10l-5 6H5z"/><path d="M19 14h-5v6"/><path d="M8 8h8M8 11h5"/>',
  'object-group':
    '<rect x="3" y="4" width="18" height="16" rx="2" stroke-dasharray="4 3"/><rect x="6" y="7" width="5" height="5" rx="1"/><rect x="13" y="12" width="5" height="5" rx="1"/>',
  // A box with an arrow out of it: a link that opens in a new tab.
  'external-link':
    '<rect x="3" y="6" width="12" height="12" rx="1.5"/><path d="M17 7h4v4M21 7l-6 6"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  'arrow-up': '<path d="M12 19V5M6 11l6-6 6 6"/>',
  'arrow-down': '<path d="M12 5v14M6 13l6 6 6-6"/>',
  'arrow-left': '<path d="M19 12H5M11 6l-6 6 6 6"/>',
  'chevrons-left': '<path d="M12 6l-6 6 6 6M19 6l-6 6 6 6"/>',
  'chevrons-right': '<path d="M12 6l6 6-6 6M5 6l6 6-6 6"/>',
  trash:
    '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/><path d="M10 11v6M14 11v6"/>',
  undo: '<path d="M9 7H5V3"/><path d="M5 7a8 8 0 1 1-1 8"/>',
  redo: '<path d="M15 7h4V3"/><path d="M19 7a8 8 0 1 0 1 8"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h8"/>',
  // A window with a side column, and the way it goes: hiding and showing
  // the editor's side columns.
  'panel-left-close':
    '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16M16 9l-3 3 3 3"/>',
  'panel-left-open':
    '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16M13 9l3 3-3 3"/>',
  'panel-right-close':
    '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M15 4v16M8 9l3 3-3 3"/>',
  'panel-right-open':
    '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M15 4v16M11 9l-3 3 3 3"/>',
  paste:
    '<path d="M9 4h6v3H9z"/><path d="M8 5H6a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V6a1 1 0 0 0-1-1h-2"/>',
  save: '<path d="M5 4h11l3 3v13H5z"/><path d="M8 4v6h8V4M8 20v-6h8v6"/>',
  download: '<path d="M12 4v11M8 11l4 4 4-4"/><path d="M5 19h14"/>',
  upload: '<path d="M12 20V9M8 13l4-4 4 4"/><path d="M5 4h14"/>',
  layout:
    '<circle cx="12" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="19" r="2"/><path d="M12 7v5M12 12H6v5M12 12h6v5"/>',
  link: '<path d="M10 14a4 4 0 0 0 6 0l2-2a4 4 0 0 0-6-6l-1 1"/><path d="M14 10a4 4 0 0 0-6 0l-2 2a4 4 0 0 0 6 6l1-1"/>',
  // A shared segment with hosts on it, for a diagram's networks.
  network:
    '<path d="M3 12h18"/><path d="M6 12V8.5M12 12v3.5M18 12V8.5"/><circle cx="6" cy="6.5" r="2"/><circle cx="12" cy="17.5" r="2"/><circle cx="18" cy="6.5" r="2"/>',
  // Fit to view's corners around a turn back: Reset view.
  'reset-view':
    '<path d="M3 8V3h5M16 3h5v5M21 16v5h-5M8 21H3v-5"/><path d="M8 12a4 4 0 1 0 1.2-2.85"/><path d="M8.6 6.6v2.8h2.8"/>',
  // A two-way diagonal arrow: the minimap's resize handle.
  'resize-diagonal': '<path d="M5 11V5h6M19 13v6h-6M5 5l14 14"/>',
  // Corners out at the edges, and turned back in: Fit diagram to view,
  // and Restore previous view after it.
  'fit-view': '<path d="M4 9V4h5M15 4h5v5M20 15v5h-5M9 20H4v-5"/>',
  'fit-view-restore': '<path d="M9 3v6H3M15 3v6h6M21 15h-6v6M3 15h6v6"/>',
  // Arrows out to the corners, and back in: Focus mode and its exit.
  focus: '<path d="M15 3h6v6M9 21H3v-6"/><path d="M21 3l-7 7M3 21l7-7"/>',
  'focus-exit':
    '<path d="M4 14h6v6M20 10h-6V4"/><path d="M14 10l7-7M3 21l7-7"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5L19 19M19 5l-1.5 1.5M6.5 17.5L5 19"/>',
  moon: '<path d="M20 14a8 8 0 1 1-10-10 7 7 0 0 0 10 10z"/>',
  system:
    '<rect x="3" y="5" width="18" height="12" rx="2"/><path d="M8 21h8"/>',
  warning: '<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17h.01"/>',
  check: '<path d="M5 13l4 4 10-10"/>',
  close: '<path d="M6 6l12 12M18 6L6 18"/>',
  search: '<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>',
  help: '<circle cx="12" cy="12" r="9"/><path d="M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.6v.6M12 17h.01"/>',
  // A clock with an arrow turning back: Draft History.
  history:
    '<path d="M4 12a8 8 0 1 0 2.34-5.66L4 8.5"/><path d="M4 4v4.5h4.5"/><path d="M12 8v4l3 2"/>',
  refresh:
    '<path d="M4 12a8 8 0 0 1 13.7-5.6L20 8"/><path d="M20 4v4h-4"/><path d="M20 12a8 8 0 0 1-13.7 5.6L4 16"/><path d="M4 20v-4h4"/>',
  // A turn back: restore a snapshot.
  restore:
    '<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>',
  publish: '<path d="M12 19V6M7 11l5-5 5 5"/><path d="M5 21h14"/>',
  image:
    '<rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="M5 17l5-4 4 3 3-2 2 3"/>',
  document:
    '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/><path d="M9 12h6M9 16h6"/>',
  group:
    '<rect x="3" y="4" width="18" height="16" rx="2" stroke-dasharray="4 3"/>',
  ungroup:
    '<rect x="3" y="4" width="8" height="8" rx="1.5"/><rect x="13" y="12" width="8" height="8" rx="1.5"/>',
  // A group gathering nodes on its own: Auto-group.
  'auto-group':
    '<rect x="3" y="4" width="18" height="16" rx="2" stroke-dasharray="4 3"/><circle cx="9" cy="10" r="1.6"/><circle cx="15" cy="10" r="1.6"/><circle cx="12" cy="15" r="1.6"/>',
  // A menu button's arrow.
  'chevron-down': '<path d="M6 9l6 6 6-6"/>',
  keyboard:
    '<rect x="2" y="6" width="20" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M8 14h8"/>',
  // A gear: Settings.
  settings:
    '<path d="M18.7 9.8L21 10v4l-2.3.2-.5 1 1.5 1.8-2.7 2.7-1.8-1.5-1 .5L14 21h-4l-.2-2.3-1-.5L7 19.7 4.3 17l1.5-1.8-.5-1L3 14v-4l2.3-.2.5-1L4.3 7 7 4.3l1.8 1.5 1-.5L10 3h4l.2 2.3 1 .5L17 4.3 19.7 7l-1.5 1.8z"/><circle cx="12" cy="12" r="3"/>',
  command: COMMAND_KEY,
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  pencil: '<path d="M4 20l1-4L16 5l3 3L8 19z"/><path d="M14 7l3 3"/>',
  // An open eye: View, for an item that is read only.
  eye: '<path d="M2.5 12C5 7.5 8.3 5.5 12 5.5s7 2 9.5 6.5c-2.5 4.5-5.8 6.5-9.5 6.5S5 16.5 2.5 12z"/><circle cx="12" cy="12" r="3"/>',
  // An eye with a line across it: a hidden description.
  'eye-off':
    '<path d="M2.5 12C5 7.5 8.3 5.5 12 5.5s7 2 9.5 6.5c-2.5 4.5-5.8 6.5-9.5 6.5S5 16.5 2.5 12z"/><circle cx="12" cy="12" r="3"/><path d="M4 4l16 16"/>',
  // A folder with its front tipped open: Open.
  'folder-open':
    '<path d="M4 19.5A1.5 1.5 0 0 1 2.5 18V5.5A1.5 1.5 0 0 1 4 4h4.5l2 2.5H18a1.5 1.5 0 0 1 1.5 1.5v2"/><path d="M4 19.5l2.6-8a1.5 1.5 0 0 1 1.4-1h12.5a1 1 0 0 1 1 1.3l-2.3 6.6a1.5 1.5 0 0 1-1.4 1.1z"/>',
  // A five-pointed star: the default layer set.
  star: '<path d="M12 2.8l2.8 5.8 6.3.9-4.6 4.4 1.1 6.3L12 17.2l-5.6 3 1.1-6.3-4.6-4.4 6.3-.9z"/>',
  // A flask: the experiment a diagram was published with.
  experiment:
    '<path d="M9 3h6M10 3v6l-5 9a2 2 0 0 0 1.8 3h10.4a2 2 0 0 0 1.8-3l-5-9V3"/><path d="M7.5 15h9"/>',
  // Book spines on a shelf, one leaning: the Node Templates library.
  library: '<path d="M4 4v16M8 8v12M12 6v14M16 6l4 14"/>',
  // Three stacked sheets: the diagram's layers.
  'layer-group':
    '<path d="M12 3l9 4.5-9 4.5-9-4.5z"/><path d="M3 12l9 4.5 9-4.5"/><path d="M3 16.5l9 4.5 9-4.5"/>',
  // The drawings of Nodes & Objects: a rectangle, a circle, and a line with a
  // bend and an arrowhead.
  rectangle: '<rect x="3" y="6" width="18" height="12" rx="1"/>',
  circle: '<circle cx="12" cy="12" r="8"/>',
  line: '<path d="M3 19l7-7h4l6-6"/><path d="M15 6h5v5"/>',
};

/** The drawing of a built-in icon, or the server icon for an unknown name. */
export function iconPath(name) {
  return Object.hasOwn(PATHS, name) ? PATHS[name] : PATHS.server;
}
