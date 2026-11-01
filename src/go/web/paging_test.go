package web

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/web/proto"
)

// The experiment, VM and disk lists page with pageNum and perPage as the experiment VM and file lists do,
// returning everything without them, and refuse a page that is not a whole number of at least 1.
func TestListsRefuseBadPages(t *testing.T) {
	useTestExperiment(t)

	server := serveAs(t, "alice", adminRole()).URL

	for _, path := range []string{"/api/v1/experiments", "/api/v1/vms", "/api/v1/disks"} {
		for _, query := range []string{"pageNum=0&perPage=10", "pageNum=1&perPage=0", "pageNum=x&perPage=10"} {
			url := path + "?" + query
			if resp := do(t, http.MethodGet, server+url, ""); resp.StatusCode != http.StatusBadRequest {
				t.Errorf("GET %s = %d, want %d: %s", url, resp.StatusCode, http.StatusBadRequest, readBody(t, resp))
			}
		}
	}
}

// Pages of the running VMs follow one another in the order of the whole list, each with the whole list's total.
func TestAllVMsPages(t *testing.T) {
	var (
		small = routeExperiment(t, "vms-small", 3, true)
		large = routeExperiment(t, "vms-large", 8, true)
	)

	useTestStore(t, small, large)

	var (
		cluster = &routeCluster{head: "head", vms: map[string]routeVMs{
			small.Metadata.Name: {count: 3, host: "head"},
			large.Metadata.Name: {count: 8, host: "head"},
		}}
		_      = useRouteCluster(t, cluster)
		server = serveAs(t, "alice", routeViewerRole()).URL
	)

	var all proto.VMList
	if code := getAPI(t, server+"/api/v1/vms", &all); code != http.StatusOK {
		t.Fatalf("GET /api/v1/vms = %d, want %d", code, http.StatusOK)
	}

	var paged []string

	for page := 1; page <= 3; page++ {
		path := fmt.Sprintf("/api/v1/vms?pageNum=%d&perPage=5", page)

		var list proto.VMList
		if code := getAPI(t, server+path, &list); code != http.StatusOK {
			t.Fatalf("GET %s = %d, want %d", path, code, http.StatusOK)
		}

		if got := list.GetTotal(); got != 11 {
			t.Errorf("GET %s total = %d, want 11", path, got)
		}

		paged = append(paged, vmIDs(&list)...)
	}

	if diff := cmp.Diff(vmIDs(&all), paged); diff != "" {
		t.Errorf("GET /api/v1/vms pages mismatch with the whole list (-want +got):\n%s", diff)
	}
}

// vmIDs is each listed VM's experiment and name, in order.
func vmIDs(list *proto.VMList) []string {
	ids := make([]string, 0, len(list.GetVms()))
	for _, vm := range list.GetVms() {
		ids = append(ids, vm.GetExperiment()+"/"+vm.GetName())
	}

	return ids
}
