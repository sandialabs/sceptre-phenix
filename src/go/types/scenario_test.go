package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v2 "phenix/types/version/v2"
)

func scenarioConfig(name, topology string, apps ...any) store.Config {
	c := store.Config{
		Version:  store.APIGroup + "/v2",
		Kind:     "Scenario",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     map[string]any{"apps": apps},
	}

	if topology != "" {
		c.Metadata.Annotations = store.Annotations{"topology": topology}
	}

	return c
}

func appNames(spec ifaces.ScenarioSpec) map[string]bool {
	names := make(map[string]bool)
	for _, a := range spec.Apps() {
		names[a.Name()] = a.Disabled()
	}

	return names
}

func TestDecodeScenarioFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  store.Config
		want    map[string]bool
		wantErr bool
	}{
		{
			name:   "apps",
			config: scenarioConfig("s", "", map[string]any{"name": "ntp"}, map[string]any{"name": "soh", "disabled": true}),
			want:   map[string]bool{"ntp": false, "soh": true},
		},
		{name: "unknown version", config: store.Config{Version: store.APIGroup + "/v1", Kind: "Scenario"}, wantErr: true},
		{name: "kind without a versioned spec", config: store.Config{Version: store.APIGroup + "/v2", Kind: "Setting"}, wantErr: true},
		{name: "spec that is not a scenario", config: store.Config{Version: store.APIGroup + "/v1", Kind: "Ruleset"}, wantErr: true},
		{
			name:    "spec that does not decode",
			config:  store.Config{Version: store.APIGroup + "/v2", Kind: "Scenario", Spec: map[string]any{"apps": "x"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := types.DecodeScenarioFromConfig(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("DecodeScenarioFromConfig() = %v, want an error", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("DecodeScenarioFromConfig() error = %v", err)
			}

			if diff := cmp.Diff(tt.want, appNames(got)); diff != "" {
				t.Errorf("DecodeScenarioFromConfig() apps (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMakeCustomScenarioFromConfig(t *testing.T) {
	t.Parallel()

	c := scenarioConfig("s", "", map[string]any{"name": "ntp"}, map[string]any{"name": "soh"}, map[string]any{"name": "tap"})

	got, err := types.MakeCustomScenarioFromConfig(c, []string{"soh", "missing"})
	if err != nil {
		t.Fatalf("MakeCustomScenarioFromConfig() error = %v", err)
	}

	if diff := cmp.Diff(map[string]bool{"ntp": false, "soh": true, "tap": false}, appNames(got)); diff != "" {
		t.Errorf("MakeCustomScenarioFromConfig() apps (-want +got):\n%s", diff)
	}

	if _, err := types.MakeCustomScenarioFromConfig(store.Config{Kind: "Scenario"}, nil); err == nil {
		t.Error("MakeCustomScenarioFromConfig() of a config without a version error = nil, want an error")
	}
}

// TestCheckScenarioTopology pins only exact and comma-list matches. The check
// is a substring match, and the test does not pin partial names.
func TestCheckScenarioTopology(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		annotations store.Annotations
		topology    string
		wantErr     bool
	}{
		{name: "exact", annotations: store.Annotations{"topology": "lab"}, topology: "lab"},
		{name: "in comma list", annotations: store.Annotations{"topology": "lab,prod"}, topology: "prod"},
		{name: "other topology", annotations: store.Annotations{"topology": "lab"}, topology: "prod", wantErr: true},
		{name: "no annotation", annotations: store.Annotations{}, topology: "lab", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := types.CheckScenarioTopology("s", tt.annotations, tt.topology)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckScenarioTopology(s, %v, %s) error = %v, want error %t", tt.annotations, tt.topology, err, tt.wantErr)
			}
		})
	}
}

func storeScenario(t *testing.T, c store.Config) {
	t.Helper()

	if err := store.Create(&c); err != nil {
		t.Fatalf("store.Create(%s) error = %v", c.Metadata.Name, err)
	}
}

func TestFromScenarioConfig(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	storeScenario(t, scenarioConfig("base", "lab"))

	got, err := types.FromScenarioConfig("base", "lab")
	if err != nil {
		t.Fatalf("FromScenarioConfig(base, lab) error = %v", err)
	}

	if got.Metadata.Name != "base" {
		t.Errorf("FromScenarioConfig(base, lab) name = %q, want base", got.Metadata.Name)
	}

	for _, tt := range []struct{ name, topology string }{{"base", "prod"}, {"missing", "lab"}, {"bad/name", "lab"}} {
		if _, err := types.FromScenarioConfig(tt.name, tt.topology); err == nil {
			t.Errorf("FromScenarioConfig(%s, %s) error = nil, want an error", tt.name, tt.topology)
		}
	}
}

func TestMergeScenariosForTopology(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	storeScenario(t, scenarioConfig("base", "lab", map[string]any{
		"name":            "soh",
		"assetDir":        "/assets",
		"metadata":        map[string]any{"c2Timeout": "5m"},
		"hosts":           []any{map[string]any{"hostname": "web", "metadata": map[string]any{"k": "v"}}},
		"runPeriodically": "10m",
		"disabled":        true,
	}))
	storeScenario(t, store.Config{
		Version:  store.APIGroup + "/v2",
		Kind:     "Scenario",
		Metadata: store.ConfigMetadata{Name: "broken", Annotations: store.Annotations{"topology": "lab"}},
		Spec:     map[string]any{"apps": "x"},
	})

	spec := &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{
		{NameF: "soh", FromScenarioF: "base"},
		{NameF: "ntp", MetadataF: map[string]any{"keep": true}},
	}}

	if err := types.MergeScenariosForTopology(spec, "lab"); err != nil {
		t.Fatalf("MergeScenariosForTopology() error = %v", err)
	}

	want := []*v2.ScenarioApp{
		{
			NameF:            "soh",
			FromScenarioF:    "base",
			AssetDirF:        "/assets",
			MetadataF:        map[string]any{"c2Timeout": "5m"},
			HostsF:           []*v2.ScenarioAppHost{{HostnameF: "web", MetadataF: map[string]any{"k": "v"}}},
			RunPeriodicallyF: "10m",
			DisabledF:        true,
		},
		{NameF: "ntp", MetadataF: map[string]any{"keep": true}},
	}

	if diff := cmp.Diff(want, spec.AppsF); diff != "" {
		t.Errorf("apps after MergeScenariosForTopology() (-want +got):\n%s", diff)
	}

	for _, tt := range []struct {
		name, topology string
		app            *v2.ScenarioApp
	}{
		{name: "scenario for another topology", topology: "prod", app: &v2.ScenarioApp{NameF: "soh", FromScenarioF: "base"}},
		{name: "scenario that does not decode", topology: "lab", app: &v2.ScenarioApp{NameF: "soh", FromScenarioF: "broken"}},
		{name: "app not in scenario", topology: "lab", app: &v2.ScenarioApp{NameF: "tap", FromScenarioF: "base"}},
	} {
		err := types.MergeScenariosForTopology(&v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{tt.app}}, tt.topology)
		if err == nil {
			t.Errorf("MergeScenariosForTopology() with %s error = nil, want an error", tt.name)
		}
	}
}
