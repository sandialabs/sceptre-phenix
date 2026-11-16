package util

import (
	"mime"
	"strings"
	"testing"
)

// For any file name, Attachment is one header line that a browser reads back
// as an attachment of exactly that name.
func FuzzAttachment(f *testing.F) {
	for _, tc := range attachmentCases() {
		f.Add(tc.name)
	}

	f.Add("\xff\xfe.bin")

	f.Fuzz(func(t *testing.T, name string) {
		got := Attachment(name)
		if strings.ContainsAny(got, "\r\n") {
			t.Fatalf("Attachment(%q) = %q, want one header line", name, got)
		}

		disposition, params, err := mime.ParseMediaType(got)
		if err != nil || disposition != "attachment" || params["filename"] != name {
			t.Fatalf("mime.ParseMediaType(Attachment(%q) = %q) = %q, filename %q, %v; want attachment, %q",
				name, got, disposition, params["filename"], err, name)
		}
	})
}
