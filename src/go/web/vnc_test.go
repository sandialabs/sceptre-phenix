package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// getVNCPage stores test-experiment with one VM, test-vm, whose vncBanner
// annotation is banner (none when nil), and opens the VM's VNC page as a role
// that may.
func getVNCPage(t *testing.T, banner any) (*http.Response, string) {
	t.Helper()

	node := map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": "test-vm"},
		"hardware": map[string]any{"vcpus": 1, "memory": 256, "os_type": "linux"},
	}

	if banner != nil {
		node["annotations"] = map[string]any{"vncBanner": banner}
	}

	useTestStore(t, testExperiment(t, "", node))

	server := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/test-vm"))
	resp := do(t, http.MethodGet, server.URL+"/api/v1/experiments/test-experiment/vms/test-vm/vnc", "")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET vnc: status %d: %s", resp.StatusCode, body)
	}

	return resp, body
}

var (
	topBannerText    = regexp.MustCompile(`<h3 class="banner-top">(.*)</h3>`)
	bottomBannerText = regexp.MustCompile(`<h3 class="banner-bottom">(.*)</h3>`)
)

// banners returns the HTML inside the page's top and bottom banners, "" for a
// banner the page leaves out.
func banners(body string) (string, string) {
	var top, bottom string

	if m := topBannerText.FindStringSubmatch(body); m != nil {
		top = m[1]
	}

	if m := bottomBannerText.FindStringSubmatch(body); m != nil {
		bottom = m[1]
	}

	return top, bottom
}

// The vncBanner annotation is shown as text: markup in it is escaped, a string
// splits into lines at its newlines, and the map form sets each banner.
func TestVNCBannerIsText(t *testing.T) {
	tests := []struct {
		name               string
		banner             any
		wantTop, wantBotom string
	}{
		{
			name:      "no annotation names the experiment and VM",
			wantTop:   "EXP: test-experiment - VM: test-vm",
			wantBotom: "EXP: test-experiment - VM: test-vm",
		},
		{
			name:      "markup in a string",
			banner:    `<img src=x onerror="alert(1)">`,
			wantTop:   "&lt;img src=x onerror=&#34;alert(1)&#34;&gt;",
			wantBotom: "&lt;img src=x onerror=&#34;alert(1)&#34;&gt;",
		},
		{
			name:      "a string's lines",
			banner:    "UNCLASSIFIED\nlab use only",
			wantTop:   "UNCLASSIFIED<br>lab use only",
			wantBotom: "UNCLASSIFIED<br>lab use only",
		},
		{
			name: "markup in the map form's lines",
			banner: map[string]any{
				"topBanner":    map[string]any{"banner": []any{"<b>top</b>", "second"}},
				"bottomBanner": map[string]any{"banner": []any{"</h3><script>alert(1)</script>"}},
			},
			wantTop:   "&lt;b&gt;top&lt;/b&gt;<br>second",
			wantBotom: "&lt;/h3&gt;&lt;script&gt;alert(1)&lt;/script&gt;",
		},
		{
			name:   "disabled",
			banner: map[string]any{"disabled": true, "topBanner": map[string]any{"banner": []any{"top"}}},
		},
		{
			name:   "a map that does not decode",
			banner: map[string]any{"topBanner": "top"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, body := getVNCPage(t, tt.banner)

			top, bottom := banners(body)
			if top != tt.wantTop || bottom != tt.wantBotom {
				t.Errorf("banners = %q, %q; want %q, %q", top, bottom, tt.wantTop, tt.wantBotom)
			}
		})
	}
}

// The map form sets only banners: keys naming other page settings, such as
// where its scripts load from or the token it connects with, are ignored, and
// colors that are not CSS color names or hex colors fall back to the defaults.
func TestVNCBannerMapSetsOnlyBanners(t *testing.T) {
	_, body := getVNCPage(t, map[string]any{
		"basePath": "https://attacker.example/",
		"token":    "attacker-token",
		"BasePath": "https://attacker.example/",
		"topBanner": map[string]any{
			"banner":          []any{"top"},
			"backgroundColor": "#a91f3d",
			"textColor":       "#FFF",
		},
		"bottomBanner": map[string]any{
			"banner":          []any{"bottom"},
			"backgroundColor": "red;} body { display: none",
			"textColor":       "url(https://attacker.example/x)",
		},
	})

	for _, leaked := range []string{"attacker.example", "attacker-token"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the page contains %q from the annotation", leaked)
		}
	}

	if !strings.Contains(body, `<script type="module" src="`+o.basePath+`novnc/app/ui.js">`) {
		t.Error("the page no longer loads noVNC from the server's base path")
	}

	for _, want := range []string{
		"background-color: #a91f3d;",
		"color: #FFF;",
		// the bottom banner's colors are not CSS colors
		"background-color: white;",
		"color: black;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page's banner styles lack %q", want)
		}
	}
}

// The page's Content-Security-Policy lets it run only scripts from this server
// and its own inline script, which carries a nonce new to each request.
func TestVNCPageContentSecurityPolicy(t *testing.T) {
	inlineNonce := regexp.MustCompile(`<script nonce="([^"]+)">`)

	nonces := make(map[string]bool)

	for range 2 {
		resp, body := getVNCPage(t, "lab")

		policy := resp.Header.Get("Content-Security-Policy")

		m := inlineNonce.FindStringSubmatch(body)
		if m == nil {
			t.Fatal("the page's inline script has no nonce")
		}

		nonce := m[1]
		nonces[nonce] = true

		for _, want := range []string{
			"default-src 'self'",
			"script-src 'self' 'nonce-" + nonce + "'",
			"connect-src 'self'",
			"object-src 'none'",
			"base-uri 'none'",
		} {
			if !strings.Contains(policy, want) {
				t.Errorf("Content-Security-Policy %q lacks %q", policy, want)
			}
		}
	}

	if len(nonces) != 2 {
		t.Error("two requests got the same nonce")
	}
}
