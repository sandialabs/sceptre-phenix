package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/websocket"

	ft "phenix/web/forward/forwardtypes"
)

const socketDirMode = 0o700

// socketPath gives the path of the unix socket that the serve command and the
// client commands share. Tests replace it.
var socketPath = func() string { //nolint:gochecknoglobals // tests replace it
	return filepath.Join(os.TempDir(), "phenix", "tunneler.sock")
}

// startUnixSocket listens on socketPath and serves each connection. Close the
// returned listener to stop it.
func startUnixSocket() (net.Listener, error) {
	var (
		usockPath = socketPath()
		usockDir  = filepath.Dir(usockPath)
	)

	if err := os.MkdirAll(usockDir, socketDirMode); err != nil {
		return nil, fmt.Errorf("creating socket directory: %w", err)
	}

	_ = os.Remove(usockPath)

	usock, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", usockPath) //nolint:exhaustruct // partial initialization
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			conn, err := usock.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: accepting connection on unix socket: %v", err)

				continue
			}

			go handleConnection(conn)
		}
	}()

	return usock, nil
}

//nolint:funlen // handler
func handleConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	var (
		enc = json.NewEncoder(conn)
		dec = json.NewDecoder(conn)
	)

	var msg Message

	err := dec.Decode(&msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: decoding message: %v\n", err)

		return
	}

	switch msg.Type {
	case LISTENERS:
		msg.Payload = marshalPayload(localListeners.snapshot())

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case MOVE:
		var args listenerAction

		if err := json.Unmarshal(msg.Payload, &args); err != nil {
			msg.Error = "malformed move arguments provided"
		} else {
			var (
				id   = args.ID
				port = args.Port
			)

			if err := validateListenerID(id); err != nil {
				msg.Error = err.Error()
			} else if err := validateLocalPort(port); err != nil {
				msg.Error = err.Error()
			} else {
				err := localListeners.withListener(id, func(listener *LocalListener) error {
					err := moveLocalListener(listener, port)
					if err != nil {
						msg.Error = fmt.Sprintf("moving listener %d to port %d: %v", id, port, err)
					}

					return err
				})

				if errors.Is(err, errListenerNotFound) {
					msg.Error = fmt.Sprintf("listener %d not found", id)
				}

				if err == nil {
					broadcastWebListeners()
				}
			}
		}

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case ACTIVATE:
		var args listenerAction
		if err := json.Unmarshal(msg.Payload, &args); err != nil {
			msg.Error = "malformed listener ID provided"
		} else {
			id := args.ID
			if err := validateListenerID(id); err != nil {
				msg.Error = err.Error()
			} else {
				err := localListeners.withListener(id, func(listener *LocalListener) error {
					if listener.Listening {
						msg.Error = fmt.Sprintf("listener %d is already active", id)
						return nil
					}

					err := activateLocalListener(listener)
					if err != nil {
						msg.Error = fmt.Sprintf("activating listener %d: %v", id, err)
					}

					return err
				})

				if errors.Is(err, errListenerNotFound) {
					msg.Error = fmt.Sprintf("listener %d not found", id)
				}

				if err == nil {
					broadcastWebListeners()
				}
			}
		}

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case DEACTIVATE:
		var args listenerAction
		if err := json.Unmarshal(msg.Payload, &args); err != nil {
			msg.Error = "malformed listener ID provided"
		} else {
			id := args.ID
			if err := validateListenerID(id); err != nil {
				msg.Error = err.Error()
			} else {
				err := localListeners.withListener(id, func(listener *LocalListener) error {
					if !listener.Listening {
						msg.Error = fmt.Sprintf("listener %d is already inactive", id)
						return nil
					}

					err := deactivateLocalListener(listener)
					if err != nil {
						msg.Error = fmt.Sprintf("deactivating listener %d: %v", id, err)
					}

					return err
				})

				if errors.Is(err, errListenerNotFound) {
					msg.Error = fmt.Sprintf("listener %d not found", id)
				}

				if err == nil {
					broadcastWebListeners()
				}
			}
		}

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	}
}

func getRemoteListeners() ([]ft.Listener, error) {
	fmt.Fprintln(os.Stdout, "getting list of existing listeners")

	experiments, err := getRemoteExperiments(httpCli, origin+"/api/v1/experiments")
	if err != nil {
		return nil, fmt.Errorf("getting experiments: %w", err)
	}

	var (
		listeners []ft.Listener
		errs      error
	)

	for _, exp := range experiments {
		vms, err := getRemoteVMs(httpCli, fmt.Sprintf("%s/api/v1/experiments/%s/vms", origin, exp))
		if err != nil {
			return nil, fmt.Errorf("getting VMs for experiment %s: %w", exp, err)
		}

		for _, vm := range vms {
			url := fmt.Sprintf("%s/api/v1/experiments/%s/vms/%s/forwards", origin, exp, vm)

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
			if err != nil {
				return nil, fmt.Errorf(
					"creating HTTP request for getting VM port forwards: %w",
					err,
				)
			}

			resp, err := httpCli.Do(req) //nolint:gosec // G704 the phenix server the user named
			if err != nil {
				return nil, fmt.Errorf(
					"getting port forwards for VM %s in experiment %s: %w",
					vm,
					exp,
					err,
				)
			}

			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("reading GET VM port forwards response: %w", err)
			}

			switch resp.StatusCode {
			case http.StatusOK:
				var payload struct {
					Listeners []ft.Listener `json:"listeners"`
				}

				err := json.Unmarshal(body, &payload)
				if err != nil {
					return nil, fmt.Errorf("parsing GET VM port forwards response: %w", err)
				}

				listeners = append(listeners, payload.Listeners...)
			case http.StatusForbidden: // user not allowed to get forwarded ports for this particular VM
				continue
			default:
				errs = errors.Join(
					errs,
					fmt.Errorf(
						"unexpected status code %d getting VM port forwards",
						resp.StatusCode,
					),
				)
			}
		}
	}

	return listeners, errs
}

func getRemoteExperiments(client *http.Client, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request for getting experiments: %w", err)
	}

	resp, err := client.Do(req) //nolint:gosec // G704 the phenix server the user named
	if err != nil {
		return nil, fmt.Errorf("getting experiments: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading GET experiments response: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing GET experiments response: %w", err)
	}

	var (
		experiments, _ = payload["experiments"].([]any)
		list           []string
	)

	// skip an entry that is not an object, is not running, or has no name
	for _, e := range experiments {
		exp, _ := e.(map[string]any)
		running, _ := exp["running"].(bool)
		name, _ := exp["name"].(string)

		if running && name != "" {
			list = append(list, name)
		}
	}

	return list, nil
}

func getRemoteVMs(client *http.Client, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request for getting VMs: %w", err)
	}

	resp, err := client.Do(req) //nolint:gosec // G704 the phenix server the user named
	if err != nil {
		return nil, fmt.Errorf("getting VMs: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading GET experiment VMs response: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing GET experiment VMs response: %w", err)
	}

	var (
		vms, _ = payload["vms"].([]any)
		list   []string
	)

	// skip an entry that is not an object or has no name
	for _, e := range vms {
		vm, _ := e.(map[string]any)

		if name, _ := vm["name"].(string); name != "" {
			list = append(list, name)
		}
	}

	return list, nil
}

func createLocalListener(listener ft.Listener) error {
	local, created := localListeners.add(listener)
	if !created {
		return nil
	}

	fmt.Fprintf(os.Stdout, "created new local listener for port %d\n", listener.SrcPort)

	if username == "" || username == listener.Owner {
		err := localListeners.withListener(local.ID, activateLocalListener)

		broadcastWebListeners()
		return err
	}

	broadcastWebListeners()

	return nil
}

func moveLocalListener(ll *LocalListener, port int) error {
	var (
		active  = ll.Listening
		oldPort = ll.SrcPort
	)

	if active {
		err := deactivateLocalListener(ll)
		if err != nil {
			return fmt.Errorf("deactivating listener: %w", err)
		}
	}

	ll.SrcPort = port

	if active {
		if err := activateLocalListener(ll); err != nil {
			moveErr := fmt.Errorf("reactivating listener on port %d: %w", port, err)

			ll.SrcPort = oldPort

			if restoreErr := activateLocalListener(ll); restoreErr != nil {
				return errors.Join(
					moveErr,
					fmt.Errorf("restoring listener on port %d: %w", oldPort, restoreErr),
				)
			}

			return moveErr
		}
	}

	return nil
}

func activateLocalListener(ll *LocalListener) error {
	if ll.Listening {
		return errors.New("listener already active")
	}

	localPort := ll.SrcPort

	//nolint:exhaustruct // partial initialization
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", fmt.Sprintf(":%d", ll.SrcPort))
	if err != nil {
		if strings.Contains(err.Error(), "bind: address already in use") {
			fmt.Fprintf(os.Stderr,
				"unable to activate local listener on port %d - address already in use\n",
				ll.SrcPort,
			)

			return fmt.Errorf("unable to activate local listener on port %d: address already in use", localPort)
		}

		return fmt.Errorf("listening on port %d: %w", localPort, err)
	}

	ll.listener = ln
	ll.Listening = true

	fmt.Fprintf(os.Stdout, "activated local listener on port %d\n", localPort)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// this error is expected when connection is closed
				if !strings.Contains(err.Error(), "use of closed network connection") {
					fmt.Fprintf(os.Stderr, "accepting new connection on port %d: %v\n", localPort, err)
				}

				return
			}

			go forwardConnection(conn, ll.WebSocketURL(wsEndpoint))
		}
	}()

	return nil
}

// forwardConnection copies conn to and from a websocket to wsURL, the VM's
// forward on phēnix. It closes conn and the websocket once either side ends or
// the dial fails, so the program connected to the local port sees the end.
func forwardConnection(conn net.Conn, wsURL string) {
	defer func() { _ = conn.Close() }()

	config, err := websocket.NewConfig(wsURL, origin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: creating websocket config: %v\n", err)

		return
	}

	config.Header = headers

	ws, err := websocket.DialConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: dialing websocket (%s): %v\n", wsURL, err)

		return
	}

	defer func() { _ = ws.Close() }()

	// the local side ended: close the websocket so the copy below ends too
	go func() {
		_, _ = io.Copy(ws, conn)
		_ = ws.Close()
	}()

	_, _ = io.Copy(conn, ws)
}

func deactivateLocalListener(ll *LocalListener) error {
	if !ll.Listening {
		return errors.New("listener already inactive")
	}

	_ = ll.listener.Close()

	ll.listener = nil
	ll.Listening = false

	fmt.Fprintf(os.Stdout, "deactivated local listener on port %d\n", ll.SrcPort)

	return nil
}

func deleteLocalListener(key string) {
	port, ok := localListeners.remove(key)
	if ok {
		broadcastWebListeners()
		fmt.Fprintf(os.Stdout, "deleted local listener on port %d\n", port)
	}
}
