package soh

import (
	"encoding/json"
	"testing"
	"time"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

// For any soh app metadata, decodeMetadata either fails or gives settings that
// keep its defaults and rules: reachability off unless set, no ICMP rules
// with reachability off, and the C2 timeout and concurrency as given.
func FuzzDecodeMetadata(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"c2Timeout":"30s","c2AppearGrace":"1m","c2ClientGrace":"2m","c2Concurrency":4,"startupDelay":"5s"}`,
		`{"testReachability":"full","injectICMPAllow":true}`,
		`{"skipInitialNetworkConfigTests":true,"testReachability":"sample","injectICMPAllow":true}`,
		`{"hostsToUseUUIDForC2Active":["web","all"]}`,
		`{"hostsToUseUUIDForC2Active":[1]}`,
		`{"hostsToUseUUIDForC2Active":{"web":true}}`,
		`{"c2Timeout":"soon"}`,
		`{"c2Concurrency":"four"}`,
		`{"hostFiles":{"web":["/etc/hosts"]},"hostCustomTests":{"web":[{"name":"t","testScript":"true"}]}}`,
		`{"packetCapture":{"captureHosts":{"web":["eth0"]},"elasticServer":{"ipAddress":"10.0.0.9/24"}}}`,
		`{"packetCapture":{"captureHosts":{"web":["eth0"]},"elasticServer":{"ipAddress":"fd00::9/64"}}}`,
		`{"testCustomReachability":[{"src":"a","dst":"b","proto":"tcp","port":80}]}`,
		`{"skipHosts":"web"}`,
		`null`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		var md map[string]any
		if json.Unmarshal([]byte(body), &md) != nil || md == nil {
			return
		}

		exp := &types.Experiment{
			Metadata: store.ConfigMetadata{Name: "exp"},
			Spec: &v1.ExperimentSpec{
				ExperimentNameF: "exp",
				ScenarioF:       &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "soh", MetadataF: md}}},
			},
		}

		s := newSOH()
		if err := s.decodeMetadata(exp); err != nil {
			return
		}

		m := s.md

		if m.Reachability == "" || (m.SkipNetworkConfig && m.Reachability != reachabilityOff) {
			t.Errorf("decodeMetadata(%s) reachability = %q, want it set and off when network checks are skipped", body, m.Reachability)
		}

		if m.Reachability == reachabilityOff && m.InjectICMPAllow {
			t.Errorf("decodeMetadata(%s) injects ICMP rules with reachability off", body)
		}

		wantTimeout := defaultC2Timeout
		if m.C2Timeout != "" {
			wantTimeout, _ = time.ParseDuration(m.C2Timeout)
		}

		if m.c2Timeout != wantTimeout {
			t.Errorf("decodeMetadata(%s) C2 timeout = %v, want %v", body, m.c2Timeout, wantTimeout)
		}

		wantConcurrency := defaultC2Concurrency
		if m.C2Concurrency != nil {
			wantConcurrency = *m.C2Concurrency
		}

		if m.c2Concurrency != wantConcurrency {
			t.Errorf("decodeMetadata(%s) C2 concurrency = %d, want %d", body, m.c2Concurrency, wantConcurrency)
		}

		if m.AppProfileKey == "" || m.uuidHosts == nil {
			t.Errorf("decodeMetadata(%s) profile key = %q, UUID hosts = %v; want both set", body, m.AppProfileKey, m.uuidHosts)
		}
	})
}
