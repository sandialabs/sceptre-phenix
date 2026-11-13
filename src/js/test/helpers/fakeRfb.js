// A stand-in for the noVNC RFB class, for tests that mock '@/vnc/loadRfb.js':
//
//   const { FakeRfb, rfbs } = await vi.hoisted(() => import('./helpers/fakeRfb.js'));
//   vi.mock('@/vnc/loadRfb.js', () => ({ loadRfb: async () => FakeRfb }));
//
// Each instance records what the console set and sent. The test dispatches
// the RFB events with `connect()`, `drop()`, `emit(type, detail)`.

/** Every FakeRfb made since the last resetRfbs(), oldest first. */
export const rfbs = [];

export function resetRfbs() {
  rfbs.length = 0;
}

export class FakeRfb extends EventTarget {
  constructor(target, url, options = {}) {
    super();
    this.target = target;
    this.url = url;
    this.options = options;
    this.viewOnly = false;
    this.scaleViewport = false;
    this.clipViewport = false;
    this.resizeSession = false;
    this.qualityLevel = 6;
    this.compressionLevel = 2;
    this.disconnected = false;
    this.keys = [];
    this.pasted = [];
    this.ctrlAltDel = 0;
    this.canvas = document.createElement('canvas');
    this.canvas.width = 1024;
    this.canvas.height = 768;
    this.canvas.tabIndex = -1;
    target.appendChild(this.canvas);
    rfbs.push(this);
  }

  emit(type, detail = {}) {
    this.dispatchEvent(new CustomEvent(type, { detail }));
  }

  connect() {
    this.emit('connect');
  }

  // the server closed the connection
  drop() {
    this.emit('disconnect', { clean: false });
  }

  disconnect() {
    this.disconnected = true;
    this.canvas.remove();
    this.emit('disconnect', { clean: true });
  }

  sendKey(keysym, code, down) {
    this.keys.push([keysym, code, down]);
  }

  sendCtrlAltDel() {
    this.ctrlAltDel += 1;
  }

  clipboardPasteFrom(text) {
    this.pasted.push(text);
  }

  focus() {
    this.canvas.focus();
  }

  blur() {
    this.canvas.blur();
  }

  toBlob(callback, type) {
    callback(new Blob(['png'], { type: type ?? 'image/png' }));
  }
}
