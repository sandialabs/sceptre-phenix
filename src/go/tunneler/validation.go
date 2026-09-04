package main

import (
	"fmt"
	"strconv"
)

func parseListenerID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("malformed listener ID provided (%s): %w", value, err)
	}

	if err := validateListenerID(id); err != nil {
		return 0, err
	}

	return id, nil
}

func parseLocalPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("malformed listener port provided (%s): %w", value, err)
	}

	if err := validateLocalPort(port); err != nil {
		return 0, err
	}

	return port, nil
}

func validateListenerID(id int) error {
	if id <= 0 {
		return fmt.Errorf("listener ID must be positive: %d", id)
	}

	return nil
}

func validateLocalPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("listener port must be between 1 and 65535: %d", port)
	}

	return nil
}
