package plog

import (
	"context"
	"log/slog"
	"time"
)

// auditTimestampKey holds an audit record's UTC time as ECS expects it. The
// file's own "time" is zone-less local time, which a shipper in another time
// zone would misread.
const auditTimestampKey = "@timestamp"

type auditKey struct{}

// Audit logs an audit record: type AUDIT, with an "@timestamp" in UTC. It is
// written to the system log file whatever the log level, so raising log.level
// never drops one; the console and UI handlers keep their own levels.
func Audit(l slog.Level, msg string, args ...any) {
	if logger == nil {
		return
	}

	ctx := context.WithValue(context.Background(), auditKey{}, true)
	attrs := []any{logAttrType, TypeAudit, auditTimestampKey, time.Now().UTC().Format(time.RFC3339Nano)}

	logger.Log(ctx, l, msg, append(attrs, args...)...)
}

// isAudit reports whether ctx is that of an Audit record.
func isAudit(ctx context.Context) bool {
	v, _ := ctx.Value(auditKey{}).(bool)

	return v
}
