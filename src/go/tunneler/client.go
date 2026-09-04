package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
)

type client struct {
	conn net.Conn

	enc *json.Encoder
	dec *json.Decoder
}

func newClient() (*client, error) {
	var (
		sockDir  = filepath.Join(os.TempDir(), "phenix")
		sockPath = filepath.Join(sockDir, "tunneler.sock")

		err error
	)

	cli := new(client)

	cli.conn, err = (&net.Dialer{}).DialContext(context.Background(), "unix", sockPath) //nolint:exhaustruct // partial initialization
	if err != nil {
		return nil, fmt.Errorf("dialing phenix unix socket %s: %w", sockPath, err)
	}

	cli.enc = json.NewEncoder(cli.conn)
	cli.dec = json.NewDecoder(cli.conn)

	return cli, nil
}

func (c client) close() error {
	return c.conn.Close()
}

func (c client) getLocalListeners() (Listeners, error) {
	msg := Message{ //nolint:exhaustruct // partial initialization
		MID:  int(rand.Uint64()), //nolint:gosec // weak random number generator
		Type: LISTENERS,
	}

	if err := c.roundTrip(&msg); err != nil {
		return nil, err
	}

	var payload Listeners
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return nil, errors.New("decoding listeners from response")
	}

	return payload, nil
}

func (c client) moveLocalListener(id, port int) error {
	msg := Message{ //nolint:exhaustruct // partial initialization
		MID:     int(rand.Uint64()), //nolint:gosec // weak random number generator
		Type:    MOVE,
		Payload: marshalPayload(listenerAction{ID: id, Port: port}),
	}

	if err := c.roundTrip(&msg); err != nil {
		return err
	}

	if msg.Error != "" {
		return fmt.Errorf("%s", msg.Error)
	}

	return nil
}

func (c client) activateLocalListener(id int) error {
	msg := Message{ //nolint:exhaustruct // partial initialization
		MID:     int(rand.Uint64()), //nolint:gosec // weak random number generator
		Type:    ACTIVATE,
		Payload: marshalPayload(
			listenerAction{ID: id}, //nolint:exhaustruct // partial initialization
		),
	}

	if err := c.roundTrip(&msg); err != nil {
		return err
	}

	if msg.Error != "" {
		return fmt.Errorf("%s", msg.Error)
	}

	return nil
}

func (c client) deactivateLocalListener(id int) error {
	msg := Message{ //nolint:exhaustruct // partial initialization
		MID:     int(rand.Uint64()), //nolint:gosec // weak random number generator
		Type:    DEACTIVATE,
		Payload: marshalPayload(
			listenerAction{ID: id}, //nolint:exhaustruct // partial initialization
		),
	}

	if err := c.roundTrip(&msg); err != nil {
		return err
	}

	if msg.Error != "" {
		return fmt.Errorf("%s", msg.Error)
	}

	return nil
}

func (c client) roundTrip(msg *Message) error {
	if err := c.enc.Encode(msg); err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	if err := c.dec.Decode(msg); err != nil {
		return fmt.Errorf("receiving response: %w", err)
	}

	return nil
}

func marshalPayload(payload any) json.RawMessage {
	data, _ := json.Marshal(payload)
	return data
}
