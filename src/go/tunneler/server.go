package main

import (
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

	ft "phenix/web/forward/forwardtypes"

	"golang.org/x/net/websocket"
)

func startUnixSocket() error {
	var (
		usockDir  = filepath.Join(os.TempDir(), "phenix")
		usockPath = filepath.Join(usockDir, "tunneler.sock")
	)

	os.MkdirAll(usockDir, 0700)
	os.Remove(usockPath)

	usock, err := net.Listen("unix", usockPath)
	if err != nil {
		return err
	}

	go func() {
		for {
			conn, err := usock.Accept()
			if err != nil {
				fmt.Printf("ERROR: accepting connection on unix socket: %v", err)
				continue
			}

			go handleConnection(conn)
		}
	}()

	return nil
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	var (
		enc = gob.NewEncoder(conn)
		dec = gob.NewDecoder(conn)
	)

	var msg Message
	if err := dec.Decode(&msg); err != nil {
		fmt.Printf("ERROR: decoding message: %v\n", err)
		return
	}

	switch msg.Type {
	case LISTENERS:
		var payload Listeners

		for _, l := range listeners {
			payload = append(payload, *l)
		}

		msg.Payload = payload

		if err := enc.Encode(msg); err != nil {
			fmt.Printf("ERROR: encoding %v message: %v\n", msg.Type, err)
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
					if err := moveLocalListener(listener, port); err != nil {
						msg.Error = fmt.Sprintf("moving listener %d to port %d: %v", id, port, err)
					}

					break
				}
			}
		} else {
			msg.Error = "malformed arguments provided"
		}

		if err := enc.Encode(msg); err != nil {
			fmt.Printf("ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case ACTIVATE:
		id, ok := msg.Payload.(int)
		if ok {
			for _, listener := range listeners {
				if listener.ID == id {
					if listener.Listening {
						msg.Error = fmt.Sprintf("listener %d is already active", id)
					} else {
						if err := activateLocalListener(listener); err != nil {
							msg.Error = fmt.Sprintf("activating listener %d: %v", id, err)
						}
					}

					break
				}
			}
		} else {
			msg.Error = "malformed listener ID provided"
		}

		if err := enc.Encode(msg); err != nil {
			fmt.Printf("ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	case DEACTIVATE:
		id, ok := msg.Payload.(int)
		if ok {
			for _, listener := range listeners {
				if listener.ID == id {
					if !listener.Listening {
						msg.Error = fmt.Sprintf("listener %d is already inactive", id)
					} else {
						if err := deactivateLocalListener(listener); err != nil {
							msg.Error = fmt.Sprintf("deactivating listener %d: %v", id, err)
						}
					}

					break
				}
			}
		} else {
			msg.Error = "malformed listener ID provided"
		}

		if err := enc.Encode(msg); err != nil {
			fmt.Printf("ERROR: encoding %v message: %v\n", msg.Type, err)
		}
	}
}

func getRemoteListeners() ([]ft.Listener, error) {
	fmt.Println("getting list of existing listeners")

	experiments, err := getRemoteExperiments(httpCli, fmt.Sprintf("%s/api/v1/experiments", origin))
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

			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				return nil, fmt.Errorf("creating HTTP request for getting VM port forwards: %w", err)
			}

			resp, err := httpCli.Do(req)
			if err != nil {
				return nil, fmt.Errorf("getting port forwards for VM %s in experiment %s: %w", vm, exp, err)
			}

			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("reading GET VM port forwards response: %w", err)
			}

			switch resp.StatusCode {
			case 200:
				var payload struct {
					Listeners []ft.Listener `json:"listeners"`
				}

				if err := json.Unmarshal(body, &payload); err != nil {
					return nil, fmt.Errorf("parsing GET VM port forwards response: %w", err)
				}

				listeners = append(listeners, payload.Listeners...)
			case 403: // user not allowed to get forwarded ports for this particular VM
				continue
			default:
				errs = errors.Join(errs, fmt.Errorf("unexpected status code %d getting VM port forwards", resp.StatusCode))
			}
		}
	}

	return listeners, errs
}

func getRemoteExperiments(client *http.Client, url string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request for getting experiments: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getting experiments: %w", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading GET experiments response: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing GET experiments response: %w", err)
	}

	var (
		experiments = payload["experiments"].([]any)
		list        []string
	)

	for _, e := range experiments {
		var (
			exp     = e.(map[string]any)
			running = exp["running"].(bool)
		)

		if running {
			list = append(list, exp["name"].(string))
		}
	}

	return list, nil
}

func getRemoteVMs(client *http.Client, url string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request for getting VMs: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getting VMs: %w", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading GET experiment VMs response: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing GET experiment VMs response: %w", err)
	}

	var (
		vms  = payload["vms"].([]any)
		list []string
	)

	for _, e := range vms {
		vm := e.(map[string]any)
		list = append(list, vm["name"].(string))
	}

	return list, nil
}

func createLocalListener(listener ft.Listener) error {
	local := &LocalListener{ID: <-listenerIDs, Listener: listener}
	listeners[listener.ToKey()] = local

	fmt.Printf("created new local listener for port %d\n", listener.SrcPort)

	if username == "" || username == listener.Owner {
		return activateLocalListener(local)
	}

	return nil
}

func moveLocalListener(ll *LocalListener, port int) error {
	active := ll.Listening

	if active {
		if err := deactivateLocalListener(ll); err != nil {
			return fmt.Errorf("deactivating listener: %w", err)
		}
	}

	ll.SrcPort = port

	if active {
		if err := activateLocalListener(ll); err != nil {
			return fmt.Errorf("reactivating listener: %w", err)
		}
	}

	return nil
}

func activateLocalListener(ll *LocalListener) error {
	if ll.Listening {
		return fmt.Errorf("listener already active")
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", ll.SrcPort))
	if err != nil {
		if strings.Contains(err.Error(), "bind: address already in use") {
			fmt.Printf("unable to activate local listener on port %d - address already in use\n", ll.SrcPort)
			return nil
		}

		return fmt.Errorf("listening on port %d: %w", ll.SrcPort, err)
	}

	ll.listener = ln
	ll.Listening = true

	fmt.Printf("activated local listener on port %d\n", ll.SrcPort)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// this error is expected when connection is closed
				if !strings.Contains(err.Error(), "use of closed network connection") {
					fmt.Printf("accepting new connection on port %d: %v\n", ll.SrcPort, err)
				}

				return
			}

			go func() {
				wsURL := ll.WebSocketURL(wsEndpoint)

				config, err := websocket.NewConfig(wsURL, origin)
				if err != nil {
					fmt.Printf("ERROR: creating websocket config: %v\n", err)
					return
				}

				config.Header = headers

				ws, err := websocket.DialConfig(config)
				if err != nil {
					fmt.Printf("ERROR: dialing websocket (%s): %v\n", wsURL, err)
					return
				}

				go io.Copy(ws, conn)
				io.Copy(conn, ws)
			}()
		}
	}()

	return nil
}

func deactivateLocalListener(ll *LocalListener) error {
	if !ll.Listening {
		return fmt.Errorf("listener already inactive")
	}

	ll.listener.Close()

	ll.listener = nil
	ll.Listening = false

	fmt.Printf("deactivated local listener on port %d\n", ll.SrcPort)

	return nil
}

func deleteLocalListener(key string) {
	ll, ok := listeners[key]

	// listener may be nil if deactivated when getting deleted
	if ok && ll.listener != nil {
		ll.listener.Close()
	}

	delete(listeners, key)

	fmt.Printf("deleted local listener on port %d\n", ll.SrcPort)
}
