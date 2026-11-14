package web

import (
	"errors"
	"net/http"

	"phenix/store"
)

// storeStatus is the HTTP status for an error from a store read: 404 when the
// record does not exist, else 500.
func storeStatus(err error) int {
	if errors.Is(err, store.ErrNotExist) {
		return http.StatusNotFound
	}

	return http.StatusInternalServerError
}
