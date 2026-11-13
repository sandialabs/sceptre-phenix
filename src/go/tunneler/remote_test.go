package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	ft "phenix/web/forward/forwardtypes"
)

// bodyTransport answers each request with status 200 and its own text as the body.
type bodyTransport string

func (b bodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(string(b))),
		Request:    req,
	}, nil
}

// bodyClient is an HTTP client that gets body from every URL, with no listener.
func bodyClient(body string) *http.Client {
	return &http.Client{Transport: bodyTransport(body)}
}

// getRemoteExperiments keeps only the named, running experiment objects and
// does not panic on other entries.
func TestGetRemoteExperiments(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{
			name: "running and stopped",
			body: `{"experiments":[{"name":"a","running":true},{"name":"b","running":false}]}`,
			want: []string{"a"},
		},
		{name: "missing running", body: `{"experiments":[{"name":"a"},{"name":"b","running":true}]}`, want: []string{"b"}},
		{name: "string running", body: `{"experiments":[{"name":"a","running":"true"}]}`},
		{name: "non-object entries", body: `{"experiments":["a",1,null,[],{"name":"b","running":true}]}`, want: []string{"b"}},
		{name: "missing name", body: `{"experiments":[{"running":true},{"name":"","running":true}]}`},
		{name: "non-string name", body: `{"experiments":[{"name":7,"running":true}]}`},
		{name: "no experiments", body: `{}`},
		{name: "experiments not a list", body: `{"experiments":{"name":"a","running":true}}`},
		{name: "not JSON", body: `<html>`, wantErr: true},
		{name: "not an object", body: `[{"name":"a","running":true}]`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := getRemoteExperiments(bodyClient(test.body), "http://phenix/api/v1/experiments")
			if (err != nil) != test.wantErr {
				t.Fatalf("getRemoteExperiments(%s) error = %v, want error %t", test.body, err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("getRemoteExperiments(%s) mismatch (-want +got):\n%s", test.body, diff)
			}
		})
	}
}

// getRemoteVMs keeps only the named VM objects and does not panic on other entries.
func TestGetRemoteVMs(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{name: "named VMs", body: `{"vms":[{"name":"a","running":false},{"name":"b"}]}`, want: []string{"a", "b"}},
		{name: "non-object entries", body: `{"vms":["a",1,null,[],{"name":"b"}]}`, want: []string{"b"}},
		{name: "missing name", body: `{"vms":[{"host":"h"},{"name":""},{"name":7}]}`},
		{name: "no VMs", body: `{"vms":null}`},
		{name: "not JSON", body: `{"vms":[`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := getRemoteVMs(bodyClient(test.body), "http://phenix/api/v1/experiments/a/vms")
			if (err != nil) != test.wantErr {
				t.Fatalf("getRemoteVMs(%s) error = %v, want error %t", test.body, err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("getRemoteVMs(%s) mismatch (-want +got):\n%s", test.body, diff)
			}
		})
	}
}

// errNoRoute is the error that remoteTransport gives for a path it does not know.
var errNoRoute = errors.New("no route for path")

// remoteRoute is the response that remoteTransport gives for one path.
type remoteRoute struct {
	status  int
	body    string
	readErr bool
}

// remoteTransport answers each request with the route of its URL path.
type remoteTransport map[string]remoteRoute

func (r remoteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	route, ok := r[req.URL.Path]
	if !ok {
		return nil, errNoRoute
	}

	body := io.Reader(strings.NewReader(route.body))
	if route.readErr {
		body = iotest.ErrReader(errNoRoute)
	}

	status := route.status
	if status == 0 {
		status = http.StatusOK
	}

	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(body), Request: req}, nil
}

// getRemoteListeners collects the forwards of each VM in each running
// experiment, skips the VMs the user cannot see, and stops at a failed request.
func TestGetRemoteListeners(t *testing.T) {
	const (
		experiments = "/api/v1/experiments"
		vmsA        = "/api/v1/experiments/a/vms"
		vmsB        = "/api/v1/experiments/b/vms"
		forwardsV1  = "/api/v1/experiments/a/vms/v1/forwards"
		forwardsV2  = "/api/v1/experiments/a/vms/v2/forwards"
		forwardsV3  = "/api/v1/experiments/b/vms/v3/forwards"
	)

	var (
		runningA  = remoteRoute{body: `{"experiments":[{"name":"a","running":true},{"name":"c","running":false}]}`}
		twoVMs    = remoteRoute{body: `{"vms":[{"name":"v1"},{"name":"v2"}]}`}
		listener1 = ft.Listener{Exp: "a", VM: "v1", SrcPort: 2222, DstHost: "10.0.0.1", DstPort: 22, Owner: "alice"}
		listener2 = ft.Listener{Exp: "b", VM: "v3", SrcPort: 8080, DstHost: "10.0.0.3", DstPort: 80, Owner: "bob"}
		listener3 = ft.Listener{Exp: "b", VM: "v3", SrcPort: 8443, DstHost: "10.0.0.3", DstPort: 443, Owner: "bob"}
	)

	tests := []struct {
		name    string
		origin  string
		routes  remoteTransport
		want    []ft.Listener
		wantErr bool
		wantIs  error
	}{
		{
			name: "every allowed VM",
			routes: remoteTransport{
				experiments: {body: `{"experiments":[{"name":"a","running":true},{"name":"b","running":true}]}`},
				vmsA:        twoVMs,
				vmsB:        {body: `{"vms":[{"name":"v3"}]}`},
				forwardsV1: {body: `{"listeners":[{"exp":"a","vm":"v1","srcPort":2222,"dstHost":"10.0.0.1",` +
					`"dstPort":22,"owner":"alice"}]}`},
				forwardsV2: {status: http.StatusForbidden},
				forwardsV3: {body: `{"listeners":[{"exp":"b","vm":"v3","srcPort":8080,"dstHost":"10.0.0.3",` +
					`"dstPort":80,"owner":"bob"},{"exp":"b","vm":"v3","srcPort":8443,"dstHost":"10.0.0.3",` +
					`"dstPort":443,"owner":"bob"}]}`},
			},
			want: []ft.Listener{listener1, listener2, listener3},
		},
		{
			name: "unexpected status",
			routes: remoteTransport{
				experiments: runningA,
				vmsA:        twoVMs,
				forwardsV1:  {status: http.StatusInternalServerError},
				forwardsV2: {body: `{"listeners":[{"exp":"a","vm":"v1","srcPort":2222,"dstHost":"10.0.0.1",` +
					`"dstPort":22,"owner":"alice"}]}`},
			},
			want:    []ft.Listener{listener1},
			wantErr: true,
		},
		{name: "experiments request fails", routes: remoteTransport{}, wantErr: true, wantIs: errNoRoute},
		{
			name:    "experiments body fails",
			routes:  remoteTransport{experiments: {readErr: true}},
			wantErr: true,
			wantIs:  errNoRoute,
		},
		{
			name:    "VMs request fails",
			routes:  remoteTransport{experiments: runningA},
			wantErr: true,
			wantIs:  errNoRoute,
		},
		{
			name:    "VMs body fails",
			routes:  remoteTransport{experiments: runningA, vmsA: {readErr: true}},
			wantErr: true,
			wantIs:  errNoRoute,
		},
		{
			name:    "forwards request fails",
			routes:  remoteTransport{experiments: runningA, vmsA: twoVMs},
			wantErr: true,
			wantIs:  errNoRoute,
		},
		{
			name:    "forwards body fails",
			routes:  remoteTransport{experiments: runningA, vmsA: twoVMs, forwardsV1: {readErr: true}},
			wantErr: true,
			wantIs:  errNoRoute,
		},
		{
			name:    "forwards not JSON",
			routes:  remoteTransport{experiments: runningA, vmsA: twoVMs, forwardsV1: {body: "<html>"}},
			wantErr: true,
		},
		{
			name:    "origin not valid in a URL",
			origin:  "http://phenix\x01",
			routes:  remoteTransport{},
			wantErr: true,
		},
		{
			name:    "experiment name not valid in a URL",
			routes:  remoteTransport{experiments: {body: `{"experiments":[{"name":"a\u0001","running":true}]}`}},
			wantErr: true,
		},
		{
			name:    "VM name not valid in a URL",
			routes:  remoteTransport{experiments: runningA, vmsA: {body: `{"vms":[{"name":"v\u0001"}]}`}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.origin == "" {
				test.origin = "http://phenix"
			}

			swapValue(t, &origin, test.origin)
			swapValue(t, &httpCli, &http.Client{Transport: test.routes})

			got, err := getRemoteListeners()
			if (err != nil) != test.wantErr {
				t.Fatalf("getRemoteListeners() error = %v, want error %t", err, test.wantErr)
			}

			if test.wantIs != nil && !errors.Is(err, test.wantIs) {
				t.Errorf("getRemoteListeners() error = %v, want %v", err, test.wantIs)
			}

			if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("getRemoteListeners() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
