package app

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestAddContextMetadataKeepsParent is a regression test: AddContextMetadata
// wrote into the map of the parent context, so sibling contexts shared keys.
func TestAddContextMetadataKeepsParent(t *testing.T) {
	t.Parallel()

	parentMD := map[string]any{"shared": 1}
	parent := SetContextMetadata(context.Background(), parentMD)

	first := AddContextMetadata(parent, "checks", []string{"files"})
	second := AddContextMetadata(parent, "c2Timeout", "5s")

	if diff := cmp.Diff(map[string]any{"shared": 1}, parentMD); diff != "" {
		t.Errorf("parent metadata after AddContextMetadata (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any{"shared": 1}, GetContextMetadata(parent)); diff != "" {
		t.Errorf("GetContextMetadata(parent) (-want +got):\n%s", diff)
	}

	wantFirst := map[string]any{"shared": 1, "checks": []string{"files"}}
	if diff := cmp.Diff(wantFirst, GetContextMetadata(first)); diff != "" {
		t.Errorf("GetContextMetadata(first) (-want +got):\n%s", diff)
	}

	wantSecond := map[string]any{"shared": 1, "c2Timeout": "5s"}
	if diff := cmp.Diff(wantSecond, GetContextMetadata(second)); diff != "" {
		t.Errorf("GetContextMetadata(second) (-want +got):\n%s", diff)
	}
}

func TestAddContextMetadataWithoutParent(t *testing.T) {
	t.Parallel()

	ctx := AddContextMetadata(context.Background(), "key", "val")

	if diff := cmp.Diff(map[string]any{"key": "val"}, GetContextMetadata(ctx)); diff != "" {
		t.Errorf("GetContextMetadata() (-want +got):\n%s", diff)
	}

	ctx = AddContextMetadata(ctx, "key", "new")

	if diff := cmp.Diff(map[string]any{"key": "new"}, GetContextMetadata(ctx)); diff != "" {
		t.Errorf("GetContextMetadata() after a second AddContextMetadata (-want +got):\n%s", diff)
	}
}

func TestGetContextMetadataEmpty(t *testing.T) {
	t.Parallel()

	got := GetContextMetadata(context.Background())
	if got == nil || len(got) != 0 {
		t.Errorf("GetContextMetadata(background) = %v, want an empty map", got)
	}
}

func TestContextTriggers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		set     func(context.Context) context.Context
		wantUI  bool
		wantCLI bool
	}{
		{name: "none", set: func(ctx context.Context) context.Context { return ctx }},
		{name: "ui", set: SetContextTriggerUI, wantUI: true},
		{name: "cli", set: SetContextTriggerCLI, wantCLI: true},
		{
			name:    "both",
			set:     func(ctx context.Context) context.Context { return SetContextTriggerCLI(SetContextTriggerUI(ctx)) },
			wantUI:  true,
			wantCLI: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := tt.set(context.Background())

			if got := IsContextTriggerUI(ctx); got != tt.wantUI {
				t.Errorf("IsContextTriggerUI() = %t, want %t", got, tt.wantUI)
			}

			if got := IsContextTriggerCLI(ctx); got != tt.wantCLI {
				t.Errorf("IsContextTriggerCLI() = %t, want %t", got, tt.wantCLI)
			}
		})
	}
}
