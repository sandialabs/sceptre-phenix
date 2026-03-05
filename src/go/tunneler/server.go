package main

import (
	"context"
	"encoding/gob"
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

func startUnixSocket() error {
	var (
		usockDir  = filepath.Join(os.TempDir(), "phenix")
		usockPath = filepath.Join(usockDir, "tunneler.sock")
	)

	if err := os.MkdirAll(usockDir, socketDirMode); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	_ = os.Remove(usockPath)

	usock, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", usockPath) //nolint:exhaustruct // partial initialization
	if err != nil {
		return err
	}

	go func() {
		for {
			conn, err := usock.Accept()
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: accepting connection on unix socket: %v", err)

				continue
			}

			go handleConnection(conn)
		}
	}()

	return nil
}

//nolint:funlen // handler
func handleConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	var (
		enc = gob.NewEncoder(conn)
		dec = gob.NewDecoder(conn)
	)

	var msg Message

	err := dec.Decode(&msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: decoding message: %v\n", err)

		return
	}

	switch msg.Type {
	case LISTENERS:
		var payload Listeners

		for _, l := range listeners {
			payload = append(payload, *l)
		}

		msg.Payload = payload

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case MOVE:
		args, ok := msg.Payload.([]int)
		if ok {
			var (
				id   = args[0]
				port = args[1]
			)

			for _, listener := range listeners {
				if listener.ID == id {
					err := moveLocalListener(listener, port)
					if err != nil {
						msg.Error = fmt.Sprintf("moving listener %d to port %d: %v", id, port, err)
					}

					break
				}
			}
		} else {
			msg.Error = "malformed arguments provided"
		}

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case ACTIVATE:
		id, ok := msg.Payload.(int)
		if ok {
			for _, listener := range listeners {
				if listener.ID == id {
					if listener.Listening {
						msg.Error = fmt.Sprintf("listener %d is already active", id)
					} else {
						err := activateLocalListener(listener)
						if err != nil {
							msg.Error = fmt.Sprintf("activating listener %d: %v", id, err)
						}
					}

					break
				}
			}
		} else {
			msg.Error = "malformed listener ID provided"
		}

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case DEACTIVATE:
		id, ok := msg.Payload.(int)
		if ok {
			for _, listener := range listeners {
				if listener.ID == id {
					if !listener.Listening {
						msg.Error = fmt.Sprintf("listener %d is already inactive", id)
					} else {
						err := deactivateLocalListener(listener)
						if err != nil {
							msg.Error = fmt.Sprintf("deactivating listener %d: %v", id, err)
						}
					}

					break
				}
			}
		} else {
			msg.Error = "malformed listener ID provided"
		}

		err := enc.Encode(msg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case CREATE, DELETE:
		//nolint:godox // TODO
		// TODO: implement
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

			resp, err := httpCli.Do(req) //nolint:gosec // SSRF via taint analysis
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

	resp, err := client.Do(req) //nolint:gosec // SSRF via taint analysis
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

	for _, e := range experiments {
		var (
			exp, _  = e.(map[string]any)
			running = exp["running"].(bool)
		)

		if running {
			name, _ := exp["name"].(string)
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

	resp, err := client.Do(req) //nolint:gosec // SSRF via taint analysis
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

	for _, e := range vms {
		vm, _ := e.(map[string]any)
		name, _ := vm["name"].(string)
		list = append(list, name)
	}

	return list, nil
}

func createLocalListener(listener ft.Listener) error {
	local := &LocalListener{ID: <-listenerIDs, Listener: listener} //nolint:exhaustruct // partial initialization
	listeners[listener.ToKey()] = local

	fmt.Fprintf(os.Stdout, "created new local listener for port %d\n", listener.SrcPort)

	if username == "" || username == listener.Owner {
		return activateLocalListener(local)
	}

	return nil
}

func moveLocalListener(ll *LocalListener, port int) error {
	active := ll.Listening

	if active {
		err := deactivateLocalListener(ll)
		if err != nil {
			return fmt.Errorf("deactivating listener: %w", err)
		}
	}

	ll.SrcPort = port

	if active {
		err := activateLocalListener(ll)
		if err != nil {
			return fmt.Errorf("reactivating listener: %w", err)
		}
	}

	return nil
}

func activateLocalListener(ll *LocalListener) error {
	if ll.Listening {
		return errors.New("listener already active")
	}

	//nolint:exhaustruct // partial initialization
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", fmt.Sprintf(":%d", ll.SrcPort))
	if err != nil {
		if strings.Contains(err.Error(), "bind: address already in use") {
			fmt.Fprintf(os.Stderr,
				"unable to activate local listener on port %d - address already in use\n",
				ll.SrcPort,
			)

			return nil
		}

		return fmt.Errorf("listening on port %d: %w", ll.SrcPort, err)
	}

	ll.listener = ln
	ll.Listening = true

	fmt.Fprintf(os.Stdout, "activated local listener on port %d\n", ll.SrcPort)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// this error is expected when connection is closed
				if !strings.Contains(err.Error(), "use of closed network connection") {
					fmt.Fprintf(os.Stderr, "accepting new connection on port %d: %v\n", ll.SrcPort, err)
				}

				return
			}

			go func() {
				wsURL := ll.WebSocketURL(wsEndpoint)

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

				go func() { _, _ = io.Copy(ws, conn) }()

				_, _ = io.Copy(conn, ws)
			}()
		}
	}()

	return nil
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
	ll, ok := listeners[key]

	// listener may be nil if deactivated when getting deleted
	if ok && ll.listener != nil {
		_ = ll.listener.Close()
	}

	delete(listeners, key)

	fmt.Fprintf(os.Stdout, "deleted local listener on port %d\n", ll.SrcPort)
}
