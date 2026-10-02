package util

import "mime"

// Attachment is the Content-Disposition header value that has a browser save
// a response as a file with the given name, quoted or encoded as RFC 6266 and
// RFC 2231 need.
func Attachment(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}
