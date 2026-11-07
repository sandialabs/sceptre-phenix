package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"phenix/store"
)

func TestStoreStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not exist", store.ErrNotExist, http.StatusNotFound},
		{"wrapped not exist", fmt.Errorf("getting config: %w", store.ErrNotExist), http.StatusNotFound},
		{"other", errors.New("bolt: database not open"), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := storeStatus(tc.err); got != tc.want {
				t.Errorf("storeStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
