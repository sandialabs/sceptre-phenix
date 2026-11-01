package web

import (
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/websocket"
	"pgregory.net/rapid"

	"phenix/web/util"
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

// vncBannerCSS finds a banner rule's color in the VNC page's styles.
var vncBannerCSS = regexp.MustCompile(`(?s)(div|h3)\.banner-(top|bottom) \{[^}]*?\s(background-color|color): ([^;]*);`)

// vncBannerHTML finds a banner's lines in the VNC page, newlines included.
var vncBannerHTML = regexp.MustCompile(`(?s)<h3 class="banner-(top|bottom)">(.*?)</h3>`)

// genVNCBannerText draws text heavy in markup and newlines; html/template
// writes NUL as U+FFFD, so there is none.
func genVNCBannerText() *rapid.Generator[string] {
	return rapid.StringOf(rapid.OneOf(
		rapid.SampledFrom([]rune("<>&\"'+=/;:{}()\n\r\t abcAB")),
		rapid.Rune().Filter(func(r rune) bool { return r != 0 }),
	))
}

// vncBannerColor is a banner color in an annotation and the color the page
// should show for it.
type vncBannerColor struct {
	given, shown string
}

// genVNCBannerColor draws CSS color names and hex colors, kept as they are,
// and unset, malformed or injected colors, which show fallback.
func genVNCBannerColor(fallback string) *rapid.Generator[vncBannerColor] {
	return rapid.Custom(func(t *rapid.T) vncBannerColor {
		switch rapid.IntRange(0, 2).Draw(t, "kind") {
		case 0:
			c := rapid.OneOf(
				rapid.SampledFrom([]string{"red", "Navy", "rebeccapurple", "transparent", "currentColor"}),
				rapid.StringMatching(`#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})`),
			).Draw(t, "css")

			return vncBannerColor{given: c, shown: c}
		case 1:
			return vncBannerColor{given: "", shown: fallback}
		default:
			c := rapid.OneOf(
				rapid.StringMatching(`[a-z#0-9]{0,4}[;:(){} "'/\\<>,.!@-][a-z0-9 ;(){}]{0,8}`),
				rapid.StringMatching(`#([0-9a-f]{1,2}|[0-9a-f]{5}|[0-9a-f]{7}|[0-9a-f]{9})`),
				rapid.StringMatching(`[a-z]{33,36}`),
			).Draw(t, "bad")

			return vncBannerColor{given: c, shown: fallback}
		}
	})
}

// The VNC page shows any banner text as text: undoing the page's escaping and
// line breaks gives the text back, and a banner's colors are CSS colors or
// the defaults.
func TestVNCBannerIsTextProperties(t *testing.T) {
	assets, err := GetAssets()
	if err != nil {
		t.Fatalf("GetAssets() error = %v", err)
	}

	page := util.NewBinaryFileSystem(assets)

	rapid.Check(t, func(t *rapid.T) {
		var (
			annotation any
			want       = map[string]string{}
			colors     = map[string]string{
				"div top background-color": defaultBannerBackground, "div bottom background-color": defaultBannerBackground,
				"h3 top color": defaultBannerText, "h3 bottom color": defaultBannerText,
			}
		)

		if rapid.Bool().Draw(t, "map form") {
			banners := map[string]any{}

			for _, side := range []string{"top", "bottom"} {
				lines := rapid.SliceOfN(genVNCBannerText(), 0, 3).Draw(t, side+" lines")
				background := genVNCBannerColor(defaultBannerBackground).Draw(t, side+" background")
				text := genVNCBannerColor(defaultBannerText).Draw(t, side+" text color")

				banner := make([]any, 0, len(lines))
				for _, line := range lines {
					banner = append(banner, line)
				}

				banners[side+"Banner"] = map[string]any{
					"banner": banner, "backgroundColor": background.given, "textColor": text.given,
				}

				if len(lines) > 0 {
					want[side] = strings.Join(lines, "\n")
				}

				colors["div "+side+" background-color"] = background.shown
				colors["h3 "+side+" color"] = text.shown
			}

			annotation = banners
		} else {
			text := genVNCBannerText().Draw(t, "text")
			annotation, want["top"], want["bottom"] = text, text, text
		}

		config := newVNCConfig("", "test-experiment", "test-vm", "nonce")
		config.setBanners("test-vm", annotation)

		rec := httptest.NewRecorder()
		page.ServeTemplate(rec, "vnc.html", config)
		body := rec.Body.String()

		got := map[string]string{}

		for _, m := range vncBannerHTML.FindAllStringSubmatch(body, -1) {
			if markup := strings.ReplaceAll(m[2], "<br>", ""); strings.ContainsAny(markup, "<>") {
				t.Fatalf("VNC page %s banner for vncBanner %q = %q, want no markup but <br>", m[1], annotation, m[2])
			}

			got[m[1]] = html.UnescapeString(strings.ReplaceAll(m[2], "<br>", "\n"))
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("VNC page banners for vncBanner %q mismatch (-want +got):\n%s", annotation, diff)
		}

		gotColors := map[string]string{}
		for _, m := range vncBannerCSS.FindAllStringSubmatch(body, -1) {
			gotColors[m[1]+" "+m[2]+" "+m[3]] = m[4]
		}

		if diff := cmp.Diff(colors, gotColors); diff != "" {
			t.Fatalf("VNC page banner colors for vncBanner %q mismatch (-want +got):\n%s", annotation, diff)
		}
	})
}
