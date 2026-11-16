package v2

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	ifaces "phenix/types/interfaces"
)

func TestScenarioSpecNilReceiver(t *testing.T) {
	t.Parallel()

	var ss *ScenarioSpec

	if ss.Apps() != nil || ss.App("x") != nil {
		t.Error("Apps() or App() of a nil *ScenarioSpec is not nil")
	}
}

func TestScenarioSpecApps(t *testing.T) {
	t.Parallel()

	ss := &ScenarioSpec{}

	added := ss.AddApp("ntp")
	ss.AddApp("vrouter")

	if added.Name() != "ntp" {
		t.Errorf("AddApp(ntp).Name() = %q, want ntp", added.Name())
	}

	names := make([]string, 0, len(ss.AppsF))
	for _, a := range ss.Apps() {
		names = append(names, a.Name())
	}

	if diff := cmp.Diff([]string{"ntp", "vrouter"}, names); diff != "" {
		t.Errorf("Apps() names (-want +got):\n%s", diff)
	}

	if got := ss.App("vrouter"); got == nil || got.Name() != "vrouter" {
		t.Errorf("App(vrouter) = %v, want the vrouter app", got)
	}

	if got := ss.App("missing"); got != nil {
		t.Errorf("App(missing) = %v, want nil", got)
	}
}

func TestScenarioAppAccessors(t *testing.T) {
	t.Parallel()

	app := &ScenarioApp{NameF: "app", FromScenarioF: "other"}

	app.SetAssetDir("/assets")
	app.SetMetadata(map[string]any{"k": "v"})
	app.SetRunPeriodically("5m")
	app.SetDisabled(true)

	host := app.AddHost("h1")
	host.SetMetadata(map[string]any{"role": "web"})
	app.AddHost("h2")

	want := &ScenarioApp{
		NameF:            "app",
		FromScenarioF:    "other",
		AssetDirF:        "/assets",
		MetadataF:        map[string]any{"k": "v"},
		HostsF:           []*ScenarioAppHost{{HostnameF: "h1", MetadataF: map[string]any{"role": "web"}}, {HostnameF: "h2"}},
		RunPeriodicallyF: "5m",
		DisabledF:        true,
	}

	if diff := cmp.Diff(want, app); diff != "" {
		t.Errorf("ScenarioApp after setters (-want +got):\n%s", diff)
	}

	if app.Name() != "app" || app.FromScenario() != "other" || app.AssetDir() != "/assets" ||
		app.Metadata()["k"] != "v" || app.RunPeriodically() != "5m" || !app.Disabled() {
		t.Errorf("ScenarioApp getters = %+v", app)
	}

	hosts := app.Hosts()
	if len(hosts) != 2 || hosts[0].Hostname() != "h1" || hosts[0].Metadata()["role"] != "web" {
		t.Errorf("Hosts() = %+v, want h1 with role web and h2", hosts)
	}

	app.SetHosts([]ifaces.ScenarioAppHost{hosts[1]})

	if diff := cmp.Diff([]*ScenarioAppHost{{HostnameF: "h2"}}, app.HostsF); diff != "" {
		t.Errorf("hosts after SetHosts (-want +got):\n%s", diff)
	}
}

type appMetadata struct {
	Source string `mapstructure:"source"`
	Count  int    `mapstructure:"count"`
}

func TestScenarioAppParseMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		md      map[string]any
		want    appMetadata
		wantErr bool
	}{
		{name: "decodes", md: map[string]any{"source": "a", "count": 2}, want: appMetadata{Source: "a", Count: 2}},
		{name: "missing", wantErr: true},
		{name: "wrong type", md: map[string]any{"count": "two"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got appMetadata

			err := ScenarioApp{NameF: "app", MetadataF: tt.md}.ParseMetadata(&got)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseMetadata(%v) error = nil, want an error", tt.md)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseMetadata(%v) error = %v", tt.md, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseMetadata(%v) (-want +got):\n%s", tt.md, diff)
			}
		})
	}
}

func TestScenarioAppParseHostMetadata(t *testing.T) {
	t.Parallel()

	app := ScenarioApp{NameF: "app", HostsF: []*ScenarioAppHost{
		{HostnameF: "h1", MetadataF: map[string]any{"source": "x", "count": 1}},
		{HostnameF: "h2"},
		{HostnameF: "h3", MetadataF: map[string]any{"count": []int{1}}},
	}}

	var got appMetadata
	if err := app.ParseHostMetadata("h1", &got); err != nil {
		t.Fatalf("ParseHostMetadata(h1) error = %v", err)
	}

	if diff := cmp.Diff(appMetadata{Source: "x", Count: 1}, got); diff != "" {
		t.Errorf("ParseHostMetadata(h1) (-want +got):\n%s", diff)
	}

	for _, host := range []string{"h2", "h3", "missing"} {
		if err := app.ParseHostMetadata(host, &got); err == nil {
			t.Errorf("ParseHostMetadata(%s) error = nil, want an error", host)
		}
	}

	if err := (ScenarioApp{NameF: "empty"}).ParseHostMetadata("h1", &got); err == nil {
		t.Error("ParseHostMetadata(h1) on an app without hosts error = nil, want an error")
	}
}
