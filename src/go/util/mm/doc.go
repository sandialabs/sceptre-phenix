// Package mm contains minimega API functions. Request code passes its context to minimega via
// [Context] or [C2Context], the ctx of util/file, or mmcli.Run*Context. An action checks it only
// until its first change, and shared or broadcast work uses [context.WithoutCancel].
package mm
