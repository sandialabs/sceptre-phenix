import { LONG_TOAST_DURATION } from '@/utils/toast.js';
import { getCurrentInstance, onBeforeUnmount, reactive } from 'vue';
import { useRoute } from 'vue-router';

import * as experimentsApi from '@/api/experiments.js';
import { usePhenixStore } from '@/store';
import { useErrorNotification } from '@/utils/errorNotif';
import { serverShuttingDown, wsCloseCause } from '@/utils/freshness.js';
import { appendNetflowLines, netflowLine, netflowMarker } from './netflow.js';

// The running experiment's netflow capture: starting and stopping it on the
// server, and the stream of flows it sends while it runs. Call it from
// setup(); the stream closes as the component unmounts.
export function useNetflowCapture() {
  const { proxy } = getCurrentInstance();
  const route = useRoute();
  const experiment = () => route.params.id;

  const netflow = reactive({
    tooltip: 'Start Netflow Capture',
    capturing: false,
    starting: false,
    stopping: false,
    // captured flows and start/stop markers, oldest first
    lines: [],
  });
  let socket = null;

  const addLines = (lines) => {
    if (lines.length > 0) appendNetflowLines(netflow.lines, lines);
  };

  function closeSocket() {
    if (socket) {
      socket.close();
      socket = null;
    }
  }

  async function stopNetflow() {
    netflow.stopping = true;
    try {
      await experimentsApi.stopNetflowCapture(experiment());
      addLines([netflowMarker('Capture stopped')]);
    } catch (err) {
      if (err.response?.status !== 404) {
        useErrorNotification(err);
        return;
      }
      // the server has no capture to stop: it already stopped (from
      // another window, or with the experiment), or phenix restarted
      addLines([netflowMarker('Capture had already stopped on the server')]);
      proxy.$buefy.toast.open({
        message:
          'The netflow capture had already stopped (from another window, by the experiment stopping, or by a phenix restart).',
        type: 'is-info',
        duration: LONG_TOAST_DURATION,
      });
    } finally {
      netflow.stopping = false;
    }

    netflow.capturing = false;
    netflow.tooltip = 'Start Netflow Capture';
    closeSocket();
  }

  // Starts a capture, or with `create` false joins the one the server is
  // running, and streams its flows; stops it when `start` is false.
  async function handleNetflow(start, create = true) {
    if (netflow.starting || netflow.stopping) return;

    if (!start) {
      stopNetflow();
      return;
    }

    // the websocket upgrade 404s until the backend registers the capture,
    // so wait for the POST to finish before connecting
    if (create) {
      netflow.starting = true;
      try {
        await experimentsApi.startNetflowCapture(experiment());
      } catch (err) {
        netflow.starting = false;
        useErrorNotification(err);
        return;
      }
    }

    netflow.capturing = true;
    netflow.tooltip = 'Stop Netflow Capture';
    addLines([netflowMarker('Capture started')]);

    let path = `${import.meta.env.BASE_URL}api/v1/experiments/${experiment()}/netflow/ws`;

    let token = usePhenixStore().token;
    if (token) {
      path += `?token=${token}`;
    }

    let proto = location.protocol == 'https:' ? 'wss://' : 'ws://';
    let url = proto + location.host + path;

    const opened = new WebSocket(url);
    socket = opened;
    const started = () => (netflow.starting = false);
    opened.addEventListener('open', started);
    opened.addEventListener('error', started);
    opened.addEventListener('close', (event) => {
      // a socket the page closed or replaced, or a normal close
      if (socket !== opened || event.code === 1000) return;
      started();
      if (wsCloseCause(event) === 'shutdown' || serverShuttingDown()) {
        // the server stops its captures as it shuts down
        socket = null;
        netflow.capturing = false;
        netflow.tooltip = 'Start Netflow Capture';
        addLines([
          netflowMarker('Capture stopped: phēnix server shutting down'),
        ]);
      } else {
        addLines([netflowMarker('Lost the connection to the phēnix server')]);
      }
    });
    opened.addEventListener('message', (event) => {
      // one update per frame, however many flows it carries
      const flows = event.data
        .split(/\r?\n/)
        .filter(Boolean)
        .map((data) => netflowLine(JSON.parse(data)));
      addLines(flows);
    });
  }

  onBeforeUnmount(closeSocket);

  return { netflow, handleNetflow, stopNetflow };
}
