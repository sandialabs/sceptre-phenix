package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrTrailingJSON is returned, wrapped, by [DecodeJSONStrict] for input that
// carries more than one JSON value.
var ErrTrailingJSON = errors.New("unexpected trailing content")

// DecodeJSONStrict decodes exactly one JSON value from reader into target,
// refusing unknown fields. Anything after the value but white space is
// refused too, with an error wrapping [ErrTrailingJSON]: a second value, or a
// stray '}' or ']', which [json.Decoder.More] misses at the top level. Any
// other error is the decoder's own, unwrapped.
func DecodeJSONStrict(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return err //nolint:wrapcheck // callers word the error themselves
	}

	var trailing json.RawMessage

	switch err := decoder.Decode(&trailing); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		const limit = 64

		if len(trailing) > limit {
			trailing = append(trailing[:limit:limit], "..."...)
		}

		return fmt.Errorf("%w: %s", ErrTrailingJSON, trailing)
	default:
		return fmt.Errorf("%w: %w", ErrTrailingJSON, err)
	}
}
