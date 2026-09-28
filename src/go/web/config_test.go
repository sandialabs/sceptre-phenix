package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/store"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// TestCreateConfigWhenEtcdIsOutOfSpace asserts a config etcd refused to store
// for lack of space is answered with 507 and says so plainly.
func TestCreateConfigWhenEtcdIsOutOfSpace(t *testing.T) {
	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Create(gomock.Any()).Return(fmt.Errorf(
		"writing config JSON to Etcd: %w: %w",
		store.ErrNoSpace, errors.New("etcdserver: mvcc: database space exceeded"),
	))

	previous := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

	body := `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"full"},` +
		`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"host"},` +
		`"hardware":{"os_type":"linux","drives":[{"image":"host.qc2"}]}}]}}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/configs", strings.NewReader(body))
	req.Header.Set("Content-Type", mimeJSON)

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, configsRole("create"))
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(CreateConfig).ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusInsufficientStorage, rec.Body.String())
	}

	var refused weberror.WebError
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if refused.Message != store.ErrNoSpace.Error() {
		t.Fatalf("message = %q, want %q", refused.Message, store.ErrNoSpace.Error())
	}
}
