// Package mm contains minimega API functions. Request code passes its context to minimega via
// [Context] or [C2Context], the ctx of api/vm, api/soh and util/file, or mmcli.Run*Context. An
// action checks it only until its first change, shared or broadcast work uses
// [context.WithoutCancel], and a web handler whose request ended writes nothing and logs at debug.
// A command that changes nothing in minimega goes through mmcli.RunReadOnly or its tabular helpers,
// with the wait line that the godoc of RunReadOnly names, so it neither waits behind nor holds up
// the shared connection. The pool does not check that the command only reads, or that its line
// is correct.
package mm
