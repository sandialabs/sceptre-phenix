package main

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
)

type client struct {
	conn net.Conn

	enc *gob.Encoder
	dec *gob.Decoder
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

	cli.enc = gob.NewEncoder(cli.conn)
	cli.dec = gob.NewDecoder(cli.conn)

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

	err := c.enc.Encode(msg)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	err = c.dec.Decode(&msg)
	if err != nil {
		return nil, fmt.Errorf("receiving response: %w", err)
	}

	if payload, ok := msg.Payload.(Listeners); ok {
		return payload, nil
	} else {
		return nil, errors.New("decoding listeners from response")
	}
}

func (c client) moveLocalListener(id, port int) error {
	msg := Message{ //nolint:exhaustruct // partial initialization
		MID:     int(rand.Uint64()), //nolint:gosec // weak random number generator
		Type:    MOVE,
		Payload: []int{id, port},
	}

	err := c.enc.Encode(msg)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	err = c.dec.Decode(&msg)
	if err != nil {
		return fmt.Errorf("receiving response: %w", err)
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
		Payload: id,
	}

	err := c.enc.Encode(msg)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	err = c.dec.Decode(&msg)
	if err != nil {
		return fmt.Errorf("receiving response: %w", err)
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
		Payload: id,
	}

	err := c.enc.Encode(msg)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	err = c.dec.Decode(&msg)
	if err != nil {
		return fmt.Errorf("receiving response: %w", err)
	}

	if msg.Error != "" {
		return fmt.Errorf("%s", msg.Error)
	}

	return nil
}
