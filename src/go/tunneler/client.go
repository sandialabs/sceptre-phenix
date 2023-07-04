package main

import (
	"encoding/gob"
	"fmt"
	"math/rand"
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

	cli.conn, err = net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("dialing phenix unix socket %s: %w", sockPath, err)
	}

	cli.enc = gob.NewEncoder(cli.conn)
	cli.dec = gob.NewDecoder(cli.conn)

	return cli, nil
}

func (this client) close() error {
	return this.conn.Close()
}

func (this client) getLocalListeners() (Listeners, error) {
	msg := Message{
		MID:  rand.Int(),
		Type: LISTENERS,
	}

	if err := this.enc.Encode(msg); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if err := this.dec.Decode(&msg); err != nil {
		return nil, fmt.Errorf("receiving response: %w", err)
	}

	if payload, ok := msg.Payload.(Listeners); ok {
		return payload, nil
	} else {
		return nil, fmt.Errorf("decoding listeners from response")
	}
}

func (this client) moveLocalListener(id, port int) error {
	msg := Message{
		MID:     rand.Int(),
		Type:    MOVE,
		Payload: []int{id, port},
	}

	if err := this.enc.Encode(msg); err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	if err := this.dec.Decode(&msg); err != nil {
		return fmt.Errorf("receiving response: %w", err)
	}

	if msg.Error != "" {
		return fmt.Errorf(msg.Error)
	}

	return nil
}

func (this client) activateLocalListener(id int) error {
	msg := Message{
		MID:     rand.Int(),
		Type:    ACTIVATE,
		Payload: id,
	}

	if err := this.enc.Encode(msg); err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	if err := this.dec.Decode(&msg); err != nil {
		return fmt.Errorf("receiving response: %w", err)
	}

	if msg.Error != "" {
		return fmt.Errorf(msg.Error)
	}

	return nil
}

func (this client) deactivateLocalListener(id int) error {
	msg := Message{
		MID:     rand.Int(),
		Type:    DEACTIVATE,
		Payload: id,
	}

	if err := this.enc.Encode(msg); err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	if err := this.dec.Decode(&msg); err != nil {
		return fmt.Errorf("receiving response: %w", err)
	}

	if msg.Error != "" {
		return fmt.Errorf(msg.Error)
	}

	return nil
}
