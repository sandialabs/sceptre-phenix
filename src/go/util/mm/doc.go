// Package mm contains minimega API functions. Request code passes its context to minimega via
// [Context] or [C2Context], the ctx of api/vm, api/soh and util/file, or mmcli.Run*Context. An
// action checks it only until its first change, shared or broadcast work uses
// [context.WithoutCancel], and a web handler whose request ended writes nothing and logs at debug.
package mm
