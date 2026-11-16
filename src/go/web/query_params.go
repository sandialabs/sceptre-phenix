package web

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"phenix/web/weberror"
)

// Every boolean and enum query parameter of the API is strict: a value that is
// not allowed, also an empty value or a bare ?name, gets a 400 (code
// invalid_request) with a message that names the allowed values. An absent
// parameter takes its default.

// sortDesc is the sortDir of a descending sort (sortAsc is the other).
const sortDesc = "desc"

// vmSortColumns are the sortCol values of the VM lists.
var vmSortColumns = []string{ //nolint:gochecknoglobals // constant list
	"name", "host", "uptime", "delayed", "labels", "network", "ipv4", "taps", "cpus", "ram", "inject_partition",
}

// fileSortColumns are the sortCol values of the experiment file list.
var fileSortColumns = []string{"name", "path", "date", "size"} //nolint:gochecknoglobals // constant list

// parseBoolQuery is the boolean query parameter name, false when the query
// does not have it.
func parseBoolQuery(q url.Values, name string) (bool, error) {
	return boolQueryOr(q, name, false)
}

// boolQueryOr is the boolean query parameter name, def when the query does not
// have it. Each value must be one that [strconv.ParseBool] takes.
func boolQueryOr(q url.Values, name string, def bool) (bool, error) {
	if !q.Has(name) {
		return def, nil
	}

	var b bool

	for i, value := range q[name] {
		v, err := strconv.ParseBool(value)
		if err != nil {
			return false, weberror.NewWebError(err, "%s must be true or false, not %q", name, value)
		}

		if i == 0 {
			b = v
		}
	}

	return b, nil
}

// enumQuery is the query parameter name, def when the query does not have it.
// Each value must be one of allowed.
func enumQuery(q url.Values, name, def string, allowed ...string) (string, error) {
	if !q.Has(name) {
		return def, nil
	}

	for _, value := range q[name] {
		if err := checkEnum(name, value, allowed...); err != nil {
			return "", err
		}
	}

	return q.Get(name), nil
}

// checkEnum returns a 400 that lists allowed unless value, of the query
// parameter name, is one of them.
func checkEnum(name, value string, allowed ...string) error {
	if slices.Contains(allowed, value) {
		return nil
	}

	return weberror.NewWebError(nil, "%s must be one of %s, not %q", name, strings.Join(allowed, ", "), value)
}

// parseSort reads sortCol, one of columns, and sortDir, asc or desc. A list
// sorts only when both are given; the column is then returned, else "".
func parseSort(q url.Values, columns []string) (string, bool, error) {
	col, err := enumQuery(q, "sortCol", "", columns...)
	if err != nil {
		return "", false, err
	}

	dir, err := enumQuery(q, "sortDir", "", sortAsc, sortDesc)
	if err != nil {
		return "", false, err
	}

	if dir == "" {
		return "", false, nil
	}

	return col, dir == sortAsc, nil
}
