package web

import (
	"net/http"
	"testing"
)

// The routes that start a job replay a retried request's 202 and Location for its Idempotency-Key, and refuse the
// key for another request with 422, as openapi.yml documents.
func TestJobRoutesHonorIdempotencyKey(t *testing.T) {
	const exp = "/api/v1/experiments/" + contractExp

	routes := []struct {
		name, path, body, other string
	}{
		{name: "VM actions", path: exp + "/vms/actions", body: `{"action":"start","vms":["test-vm"]}`,
			other: `{"action":"pause","vms":["test-vm"]}`},
		{name: "file archive", path: exp + "/files/archive", body: `{"paths":["a.log"]}`, other: `{"paths":["a.pcap"]}`},
		{name: "disk clone", path: "/api/v1/disks/clone/jobs?disk=" + contractDisk + "&new=copy"},
	}

	spec := loadOpenAPI(t)

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			useContractFixture(t)
			server := serveContractAs(t, contractAdmin(), nil).URL
			key := []string{"Idempotency-Key", "retry-" + route.name}

			first := do(t, http.MethodPost, server+route.path, route.body, key...)
			if first.StatusCode != http.StatusAccepted {
				t.Fatalf("POST %s = %d, want %d: %s", route.path, first.StatusCode, http.StatusAccepted, readBody(t, first))
			}

			retry := do(t, http.MethodPost, server+route.path, route.body, key...)
			if retry.StatusCode != http.StatusAccepted || retry.Header.Get("Location") != first.Header.Get("Location") {
				t.Errorf("retried POST %s = %d at %q, want %d at the first job's %q", route.path, retry.StatusCode,
					retry.Header.Get("Location"), http.StatusAccepted, first.Header.Get("Location"))
			}

			if got := retry.Header.Get("Idempotent-Replayed"); got != flagTrue {
				t.Errorf("retried POST %s Idempotent-Replayed = %q, want true", route.path, got)
			}

			other, body := route.path+"x", route.body
			if route.other != "" {
				other, body = route.path, route.other
			}

			if resp := do(t, http.MethodPost, server+other, body, key...); resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("POST %s with the key of another request = %d, want %d", other, resp.StatusCode,
					http.StatusUnprocessableEntity)
			}

			for _, status := range []int{http.StatusConflict, http.StatusUnprocessableEntity} {
				if !spec.documents(http.MethodPost, route.path, status) {
					t.Errorf("openapi.yml documents %d for POST %s = false, want true", status, route.path)
				}
			}
		})
	}
}
