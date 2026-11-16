package plog

import (
	"math"
	"strings"
	"time"
)

// PartFlushInterval is the time that a reader waits for the next part of a
// multi-part message. After this time, the reader logs the parts that it has.
const PartFlushInterval = 10 * time.Millisecond

// Part identifies one record of a multi-part log message. The phenix-apps
// logger writes each line of a long message as a record with "part" (1..N)
// and "parts" (N) fields.
type Part struct {
	Num   int
	Total int
}

// ParsePart returns the Part that the decoded "part" and "parts" JSON
// values give. It returns false if a value is missing or is not a whole
// number, or if num is not in the range 1 to total.
func ParsePart(num, total any) (Part, bool) {
	n, ok := wholeNumber(num)
	if !ok {
		return Part{}, false
	}

	t, ok := wholeNumber(total)
	if !ok || n < 1 || n > t {
		return Part{}, false
	}

	return Part{Num: n, Total: t}, true
}

func wholeNumber(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < 1 || f > math.MaxInt32 {
		return 0, false
	}

	return int(f), true
}

// PartJoiner joins the parts of one multi-part log message into one message.
// It keeps T, the data of the first part, for the emit function. A
// PartJoiner is not safe for concurrent use.
type PartJoiner[T any] struct {
	emit  func(level string, first T, msg string)
	level string
	first T
	total int
	next  int
	msgs  []string
}

// NewPartJoiner returns a PartJoiner that calls emit once for each joined
// message. The message is the text of the parts, joined by "\n".
func NewPartJoiner[T any](emit func(level string, first T, msg string)) *PartJoiner[T] {
	return &PartJoiner[T]{emit: emit} //nolint:exhaustruct // empty until the first part
}

// Add adds one part with its level, text and data. If the part does not
// continue the pending message, Add first emits the pending message. Add
// emits the message when the part is its last part.
func (j *PartJoiner[T]) Add(level string, part Part, msg string, data T) {
	if j.Pending() && (part.Num != j.next || part.Total != j.total || level != j.level) {
		j.Flush()
	}

	if !j.Pending() {
		j.level = level
		j.first = data
		j.total = part.Total
	}

	j.msgs = append(j.msgs, msg)
	j.next = part.Num + 1

	if part.Num == part.Total {
		j.Flush()
	}
}

// Pending returns true if the PartJoiner has parts that it did not emit.
func (j *PartJoiner[T]) Pending() bool {
	return len(j.msgs) > 0
}

// Flush emits the pending parts as one message. It does nothing if no parts
// are pending.
func (j *PartJoiner[T]) Flush() {
	if !j.Pending() {
		return
	}

	msg := strings.Join(j.msgs, "\n")
	level, first := j.level, j.first

	var zero T

	j.msgs, j.first, j.level, j.total, j.next = nil, zero, "", 0, 0

	j.emit(level, first, msg)
}
