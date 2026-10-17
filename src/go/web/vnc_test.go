package web

import (
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

// getVNCPage stores test-experiment with test-vm, whose vncBanner is banner (none if nil),
// and opens its VNC page as a permitted role.
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
		t.Fatalf("GET vms/test-vm/vnc = %d, want %d: %s", resp.StatusCode, http.StatusOK, body)
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
		name                string
		banner              any
		wantTop, wantBottom string
	}{
		{
			name:       "no annotation names the experiment and VM",
			wantTop:    "EXP: test-experiment - VM: test-vm",
			wantBottom: "EXP: test-experiment - VM: test-vm",
		},
		{
			name:       "markup in a string",
			banner:     `<img src=x onerror="alert(1)">`,
			wantTop:    "&lt;img src=x onerror=&#34;alert(1)&#34;&gt;",
			wantBottom: "&lt;img src=x onerror=&#34;alert(1)&#34;&gt;",
		},
		{
			name:       "a string's lines",
			banner:     "UNCLASSIFIED\nlab use only",
			wantTop:    "UNCLASSIFIED<br>lab use only",
			wantBottom: "UNCLASSIFIED<br>lab use only",
		},
		{
			name: "markup in the map form's lines",
			banner: map[string]any{
				"topBanner":    map[string]any{"banner": []any{"<b>top</b>", "second"}},
				"bottomBanner": map[string]any{"banner": []any{"</h3><script>alert(1)</script>"}},
			},
			wantTop:    "&lt;b&gt;top&lt;/b&gt;<br>second",
			wantBottom: "&lt;/h3&gt;&lt;script&gt;alert(1)&lt;/script&gt;",
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
			if top != tt.wantTop {
				t.Errorf("GET vms/test-vm/vnc with vncBanner %v top banner = %q, want %q", tt.banner, top, tt.wantTop)
			}

			if bottom != tt.wantBottom {
				t.Errorf("GET vms/test-vm/vnc with vncBanner %v bottom banner = %q, want %q", tt.banner, bottom, tt.wantBottom)
			}
		})
	}
}

// The map form sets only banners: other page settings are ignored, and non-CSS colors
// fall back to defaults.
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
			t.Errorf("GET vms/test-vm/vnc contains %q from the annotation, want it left out", leaked)
		}
	}

	if want := `<script type="module" src="` + o.basePath + `novnc/app/ui.js">`; !strings.Contains(body, want) {
		t.Errorf("GET vms/test-vm/vnc has no %s, want noVNC loaded from the server's base path", want)
	}

	for _, want := range []string{
		"background-color: #a91f3d;",
		"color: #FFF;",
		// the bottom banner's colors are not CSS colors
		"background-color: white;",
		"color: black;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET vms/test-vm/vnc banner styles have no %q, want it", want)
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
			t.Fatal("GET vms/test-vm/vnc inline script has no nonce, want one")
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
				t.Errorf("GET vms/test-vm/vnc Content-Security-Policy = %q, want it to contain %q", policy, want)
			}
		}
	}

	if len(nonces) != 2 {
		t.Errorf("two GET vms/test-vm/vnc nonces = %v, want two different ones", nonces)
	}
}

// A VNC websocket refused for want of vms/vnc is audited (the contract tests
// cover which roles are refused).
func TestVNCWebSocketDenialIsAudited(t *testing.T) {
	server := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/other-vm")).URL
	events := captureAudit(t)

	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/vms/test-vm/vnc/ws", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET vms/test-vm/vnc/ws without vms/vnc get = %d, want %d: %s", resp.StatusCode, http.StatusForbidden, readBody(t, resp))
	}

	wantDenied(t, events(), "vnc-attach")
}

// Opening a VM's VNC websocket is audited once it is attached.
func TestVNCWebSocketAttachIsAudited(t *testing.T) {
	// the VM's VNC server: echoes what it is sent
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for the VM's VNC server: %v", err)
	}

	t.Cleanup(func() { _ = l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		defer conn.Close()

		_, _ = io.Copy(conn, conn)
	}()

	useTestMM(t, &testMM{vnc: l.Addr().String()})

	api := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/test-vm")).URL + "/api/v1"
	events := captureAudit(t)

	ws, err := websocket.Dial(
		"ws"+strings.TrimPrefix(api, "http")+"/experiments/test-experiment/vms/test-vm/vnc/ws", "", api,
	)
	if err != nil {
		t.Fatalf("websocket.Dial(vms/test-vm/vnc/ws) error = %v", err)
	}

	defer ws.Close()

	if _, err := ws.Write([]byte("RFB")); err != nil {
		t.Fatalf("writing to vms/test-vm/vnc/ws: %v", err)
	}

	buf := make([]byte, 3)
	if _, err := io.ReadFull(ws, buf); err != nil {
		t.Errorf("reading the echo from vms/test-vm/vnc/ws: %v", err)
	} else if got := string(buf); got != "RFB" {
		t.Errorf("vms/test-vm/vnc/ws echoed %q, want RFB", got)
	}

	wantAudit(t, events(), "vnc-attach", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "test-experiment", "phenix.vm.name": "test-vm",
	})
}
