package weberror

import "net/http"

// Code is a stable, machine-readable kind of error in an Error body, for
// clients to match on rather than the message, which may change.
type Code string

// The codes an Error body carries; openapi.yml's Error schema lists them.
const (
	// CodeInvalidRequest is a request the server cannot act on as sent.
	CodeInvalidRequest Code = "invalid_request"
	// CodeUnauthorized is a request without a valid session.
	CodeUnauthorized Code = "unauthorized"
	// CodeForbidden is a request the role may not make.
	CodeForbidden Code = "forbidden"
	// CodeNotFound is a request for something that does not exist.
	CodeNotFound Code = "not_found"
	// CodeConflict is a request at odds with the current state.
	CodeConflict Code = "conflict"
	// CodeRateLimited is a request refused until the server has room again.
	CodeRateLimited Code = "rate_limited"
	// CodeUnavailable is a request the server or minimega cannot serve now.
	CodeUnavailable Code = "unavailable"
	// CodeInternal is a request that failed in the server.
	CodeInternal Code = "internal"
	// CodeBrokenDisks is a start refused because a VM's disk image or CD-ROM image is missing or its backing chain
	// is broken; details.disks lists them.
	CodeBrokenDisks Code = "broken_disks"
	// CodeDiskInUse is a disk delete refused because other images or experiments use the image; details.images and
	// details.experiments list them.
	CodeDiskInUse Code = "disk_in_use"
	// CodeVLANInUse is a start that failed because minimega gives a VLAN ID that the experiment pins to another
	// alias; details.vlan names the ID.
	CodeVLANInUse Code = "vlan_in_use"
	// CodeVNCUnreachable is a VNC probe that could not connect to the VM's VNC server; details.host and details.port
	// name the server.
	CodeVNCUnreachable Code = "vnc_unreachable"
)

// CodeFor is the code of an error answered with status.
func CodeFor(status int) Code {
	switch status {
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound, http.StatusGone:
		return CodeNotFound
	case http.StatusConflict, http.StatusPreconditionFailed, http.StatusLocked:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeUnavailable
	}

	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		return CodeInvalidRequest
	}

	return CodeInternal
}
