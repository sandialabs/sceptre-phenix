package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/store"
)

func TestLoginUnknownUserLooksLikeWrongPassword(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).Return(store.ErrNotExist).AnyTimes()

	original := store.DefaultStore

	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	store.DefaultStore = m //nolint:reassign // install test double

	const username = "nobody@example.com"

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/login",
		strings.NewReader(`{"user":"`+username+`","pass":"wrong"}`),
	)

	rec := httptest.NewRecorder()
	Login(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an unknown user, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), username) {
		t.Fatalf("response echoes the username: %q", rec.Body.String())
	}
}
