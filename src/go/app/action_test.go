package app_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"phenix/app"
)

func TestParseAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value   string
		want    app.Action
		wantErr error
	}{
		{value: "configure", want: app.ActionConfigure},
		{value: "config", want: app.ActionConfigure},
		{value: "pre-start", want: app.ActionPreStart},
		{value: "pre", want: app.ActionPreStart},
		{value: "post-start", want: app.ActionPostStart},
		{value: "post", want: app.ActionPostStart},
		{value: "running", want: app.ActionRunning},
		{value: "run", want: app.ActionRunning},
		{value: "cleanup", want: app.ActionCleanup},
		{value: "clean", want: app.ActionCleanup},
		{value: " Pre-Start\t", want: app.ActionPreStart},
		{value: "RUN", want: app.ActionRunning},
		{value: "", wantErr: app.ErrUnknownAction},
		{value: "start", wantErr: app.ErrUnknownAction},
		{value: "prestart", wantErr: app.ErrUnknownAction},
		{value: "pre start", wantErr: app.ErrUnknownAction},
		{value: "all", wantErr: app.ErrUnknownAction},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()

			got, err := app.ParseAction(tt.value)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Errorf("ParseAction(%q) = %q, %v; want %q, %v", tt.value, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// actionNames maps each stage name and shorthand, in lower case, to its stage.
func actionNames() map[string]app.Action {
	names := make(map[string]app.Action)

	for _, action := range app.Actions() {
		names[string(action)] = action
		names[action.Shorthand()] = action
	}

	return names
}

// Every stage name and shorthand parses to its stage in any letter case and
// with spaces around it, and each stage's name and shorthand round-trip.
func TestParseActionRoundTrip(t *testing.T) {
	t.Parallel()

	names := actionNames()
	tokens := make([]string, 0, len(names))

	for name := range names {
		tokens = append(tokens, name)
	}

	rapid.Check(t, func(t *rapid.T) {
		token := rapid.SampledFrom(tokens).Draw(t, "token")
		upper := rapid.SliceOfN(rapid.Bool(), len(token), len(token)).Draw(t, "upper")
		pad := rapid.StringOfN(rapid.SampledFrom([]rune(" \t\n")), 0, 3, -1)

		var cased strings.Builder

		for i, r := range token {
			if upper[i] {
				r = []rune(strings.ToUpper(string(r)))[0]
			}

			cased.WriteRune(r)
		}

		value := pad.Draw(t, "before") + cased.String() + pad.Draw(t, "after")

		got, err := app.ParseAction(value)
		if err != nil || got != names[token] {
			t.Fatalf("ParseAction(%q) = %q, %v; want %q, nil", value, got, err, names[token])
		}

		for _, text := range []string{string(got), got.Shorthand()} {
			if again, err := app.ParseAction(text); err != nil || again != got {
				t.Fatalf("ParseAction(%q) = %q, %v; want %q, nil", text, again, err, got)
			}
		}
	})
}

// Text that is not a stage name or shorthand, after trimming and lower casing,
// does not parse, and its error quotes it and lists the stages.
func TestParseActionRefusesOtherText(t *testing.T) {
	t.Parallel()

	names := actionNames()

	rapid.Check(t, func(t *rapid.T) {
		value := rapid.String().Draw(t, "value")
		if _, ok := names[strings.ToLower(strings.TrimSpace(value))]; ok {
			t.Skip("a stage name")
		}

		if err := parseActionRefusal(value); err != nil {
			t.Fatal(err)
		}
	})
}

// parseActionRefusal returns why ParseAction(value) is not a refusal that wraps
// ErrUnknownAction, quotes value and lists the stages, or nil when it is one.
func parseActionRefusal(value string) error {
	got, err := app.ParseAction(value)

	switch {
	case err == nil:
		return errors.New("ParseAction(" + strconv.Quote(value) + ") = " + string(got) + ", want an error")
	case !errors.Is(err, app.ErrUnknownAction):
		return errors.New("ParseAction(" + strconv.Quote(value) + ") error does not wrap ErrUnknownAction: " + err.Error())
	case !strings.Contains(err.Error(), strconv.Quote(value)), !strings.Contains(err.Error(), app.ActionChoices()):
		return errors.New("ParseAction(" + strconv.Quote(value) + ") error does not quote it and list the stages: " + err.Error())
	}

	return nil
}
