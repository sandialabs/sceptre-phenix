// The size limit on what the UI sends over the websocket: the server closes
// a connection that sends a longer message (maxMsgSize in broker/client.go).
const WS_MAX_BYTES = 2048;

const encoder = new TextEncoder();

// whether a message, as JSON, is short enough for the server to read
export function wsMsgFits(payload) {
  return encoder.encode(JSON.stringify(payload)).length <= WS_MAX_BYTES;
}
