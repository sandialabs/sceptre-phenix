// A stand-in for a running experiment with VNC, without minimega, for the
// VNC specs and the docs screenshots. page.route answers the experiment, its
// VM rows and their VNC sessions. page.routeWebSocket answers the VM list on
// the app websocket, and it is a small RFB 3.8 server behind each VNC
// websocket: security None, a 640x480 screen and one Raw rectangle. Each
// connection keeps the messages that its console sends, so a test sees the
// keys and the pointer events that reach the VM.

const EXP = 'e2e-vnc';
const WIDTH = 640;
const HEIGHT = 480;

// RFB client message types (RFC 6143, 7.5) and their fixed lengths
const SET_PIXEL_FORMAT = 0;
const SET_ENCODINGS = 2;
const UPDATE_REQUEST = 3;
const KEY_EVENT = 4;
const CUT_TEXT = 6;
const FIXED_LENGTH = { 0: 20, 3: 10, 4: 8, 5: 6 };

// the color that the fake server draws for each VM, as RGB
const COLORS = {
  web: [200, 40, 40],
  db: [40, 120, 200],
  plc: [160, 120, 20],
  historian: [40, 160, 80],
};

const vmRow = (name, state = 'RUNNING') => ({
  name,
  state,
  running: state === 'RUNNING',
  busy: false,
  host: 'compute-1',
  snapshot: true,
  cpus: 1,
  ram: 2048,
  disk: '/phenix/images/e2e.qc2',
  networks: ['EXP (100)'],
  ipv4: ['10.0.0.1'],
  taps: ['e2e_tap'],
  captures: [],
  uptime: 3600,
  tags: {},
});

// web and db run, plc is paused (a paused VM keeps its screen), and
// historian is shut down
const VMS = [
  vmRow('web'),
  vmRow('db'),
  vmRow('plc', 'PAUSED'),
  vmRow('historian', 'QUIT'),
];

const banner = (experiment, vm) => ({
  lines: [`EXP: ${experiment} - VM: ${vm}`],
  backgroundColor: 'white',
  textColor: 'black',
});

const session = (experiment, vm) => ({
  experiment,
  vm,
  websocket: `experiments/${experiment}/vms/${vm}/vnc/ws`,
  banners: { top: banner(experiment, vm), bottom: banner(experiment, vm) },
  display: { vga: 'std', remoteResize: false },
  clipboard: { mode: 'pasteAsKeys' },
});

// the screen of each VM: one color, so a test can check what a canvas drew
const solidColor = (vm) => COLORS[vm];

// A FramebufferUpdate with one Raw rectangle at 0,0, in the pixel format that
// noVNC asks for: 4 bytes for each pixel, red first. paint(x, y) gives the
// RGB of each pixel.
function rawUpdate(width, height, paint) {
  const head = Buffer.alloc(16);
  head.writeUInt16BE(1, 2);
  head.writeUInt16BE(width, 8);
  head.writeUInt16BE(height, 10);
  head.writeInt32BE(0, 12);
  const pixels = Buffer.alloc(width * height * 4);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      Buffer.from(paint(x, y)).copy(pixels, (y * width + x) * 4);
    }
  }
  return Buffer.concat([head, pixels]);
}

function serverInit(name) {
  const text = Buffer.from(name);
  const init = Buffer.alloc(24 + text.length);
  init.writeUInt16BE(WIDTH, 0);
  init.writeUInt16BE(HEIGHT, 2);
  // 32 bpp, depth 24, little endian, true color, max 255, shifts 16 8 0
  Buffer.from([32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0]).copy(init, 4);
  init.writeUInt32BE(text.length, 20);
  text.copy(init, 24);
  return init;
}

// The length of the client message at the start of `buf`, or 0 when it has
// not all arrived. An unknown type gives -1.
function messageLength(buf) {
  const type = buf[0];
  if (type in FIXED_LENGTH) return FIXED_LENGTH[type];
  if (type === SET_ENCODINGS) {
    return buf.length < 4 ? 0 : 4 + 4 * buf.readUInt16BE(2);
  }
  if (type === CUT_TEXT) {
    return buf.length < 8 ? 0 : 8 + Math.abs(buf.readInt32BE(4));
  }
  return -1;
}

// The fake RFB servers of one test. Each connection keeps the types of the
// messages it got, in order, and the keysyms of its key presses.
// screen(vm, x, y) gives the RGB of each pixel of the screen of a VM.
function rfbServers(screen) {
  const connections = [];

  function accept(ws, vm) {
    const conn = { vm, types: [], keysyms: [], pushed: 0, sendUpdate: null };
    connections.push(conn);
    let buf = Buffer.alloc(0);
    let stage = 'version';
    conn.sendUpdate = () => {
      conn.pushed = conn.types.length;
      ws.send(rawUpdate(1, 1, (x, y) => screen(vm, x, y)));
    };

    ws.send(Buffer.from('RFB 003.008\n'));
    ws.onMessage((message) => {
      buf = Buffer.concat([buf, Buffer.from(message)]);
      for (;;) {
        if (stage === 'version') {
          if (buf.length < 12) return;
          buf = buf.subarray(12);
          ws.send(Buffer.from([1, 1]));
          stage = 'security';
        } else if (stage === 'security') {
          if (buf.length < 1) return;
          buf = buf.subarray(1);
          ws.send(Buffer.alloc(4));
          stage = 'init';
        } else if (stage === 'init') {
          if (buf.length < 1) return;
          buf = buf.subarray(1);
          ws.send(serverInit(vm));
          stage = 'normal';
        } else {
          if (!buf.length) return;
          const length = messageLength(buf);
          if (length < 0) {
            conn.types.push(buf[0]);
            buf = Buffer.alloc(0);
            return;
          }
          if (!length || buf.length < length) return;
          const type = buf[0];
          if (type === KEY_EVENT && buf[1])
            conn.keysyms.push(buf.readUInt32BE(4));
          const first = type === UPDATE_REQUEST && !conn.types.includes(type);
          conn.types.push(type);
          buf = buf.subarray(length);
          if (first) {
            ws.send(rawUpdate(WIDTH, HEIGHT, (x, y) => screen(vm, x, y)));
          }
        }
      }
    });
  }

  return {
    connections,
    accept,
    of: (vm) => connections.filter((conn) => conn.vm === vm),
    keysymsOf: (vm) =>
      connections.flatMap((c) => (c.vm === vm ? c.keysyms : [])),
  };
}

// Stands in for the server of a running experiment with the VMS: its page,
// its VMs and their VNC sessions over REST, the VM list over the app
// websocket, and an RFB server behind each VNC websocket. Returns the RFB
// servers.
async function standInExperiment(
  page,
  { experiment = EXP, screen = solidColor } = {},
) {
  const servers = rfbServers(screen);

  await page.route(
    (url) => url.pathname.startsWith(`/api/v1/experiments/${experiment}`),
    (route) => {
      const path = new URL(route.request().url()).pathname;
      const vm = /\/vms\/([^/]+)(\/vnc)?$/.exec(path);
      if (vm) {
        const row = VMS.find((v) => v.name === vm[1]);
        if (!row) return route.fulfill({ status: 404, json: {} });
        return route.fulfill({
          json: vm[2] ? session(experiment, row.name) : row,
        });
      }
      if (path === `/api/v1/experiments/${experiment}`) {
        return route.fulfill({
          json: {
            name: experiment,
            running: true,
            vm_count: VMS.length,
            vlans: [],
            scenario: '',
            topology: 'e2e',
            start_time: new Date().toISOString(),
          },
        });
      }
      return route.fulfill({ json: {} });
    },
  );

  await page.routeWebSocket(/\/api\/v1\/ws(\?.*)?$/, (ws) => {
    ws.onMessage((message) => {
      let msg;
      try {
        msg = JSON.parse(message);
      } catch {
        return;
      }
      const { type, action } = msg.resource ?? {};
      if (type === 'experiment/vms' && action === 'list') {
        ws.send(
          JSON.stringify({
            resource: { type, name: experiment, action },
            seq: 1,
            result: {
              vms: VMS,
              total: VMS.length,
              running: 2,
              labels: [],
              hosts: ['compute-1'],
            },
          }),
        );
      }
    });
  });

  await routeVncSockets(page, servers);
  return servers;
}

// Sends each VNC websocket to the RFB servers. The other websockets of an
// experiment, such as its netflow stream, stay open and quiet.
async function routeVncSockets(page, servers) {
  const vncSocket = /\/vms\/([^/?]+)\/vnc\/ws(\?|$)/;
  await page.routeWebSocket(/\/api\/v1\/experiments\//, (ws) => {
    const vm = vncSocket.exec(ws.url());
    if (vm) servers.accept(ws, decodeURIComponent(vm[1]));
  });

  // noVNC reads the WebSocket methods and properties only from the object
  // and its direct prototype; the routed WebSocket has them further up
  await page.addInitScript(() => {
    const Routed = window.WebSocket;
    class Flat extends Routed {}
    for (const name of ['send', 'close']) {
      Object.defineProperty(Flat.prototype, name, {
        value(...args) {
          return Routed.prototype[name].apply(this, args);
        },
      });
    }
    const props = ['binaryType', 'onerror', 'onmessage', 'onopen'];
    for (const name of [...props, 'onclose', 'protocol', 'readyState']) {
      let proto = Routed.prototype;
      let descriptor;
      while (
        proto &&
        !(descriptor = Object.getOwnPropertyDescriptor(proto, name))
      ) {
        proto = Object.getPrototypeOf(proto);
      }
      if (descriptor) Object.defineProperty(Flat.prototype, name, descriptor);
    }
    window.WebSocket = Flat;
  });
}

// Stands in for the VNC sessions of the named VMs of an experiment, and for
// an RFB server behind each of their VNC websockets. Other requests go on.
// screen(vm, x, y) gives the RGB of each pixel. Returns the RFB servers.
async function standInConsoles(page, { experiment, vms, screen = solidColor }) {
  const servers = rfbServers(screen);
  const vncPath = new RegExp(
    `^/api/v1/experiments/${experiment}/vms/([^/]+)/vnc$`,
  );
  await page.route(
    (url) => vncPath.test(url.pathname),
    (route) => {
      const path = new URL(route.request().url()).pathname;
      const vm = decodeURIComponent(vncPath.exec(path)[1]);
      if (!vms.includes(vm)) return route.fallback();
      return route.fulfill({ json: session(experiment, vm) });
    },
  );
  await routeVncSockets(page, servers);
  return servers;
}

module.exports = {
  EXP,
  WIDTH,
  HEIGHT,
  COLORS,
  VMS,
  SET_PIXEL_FORMAT,
  SET_ENCODINGS,
  UPDATE_REQUEST,
  standInExperiment,
  standInConsoles,
};
