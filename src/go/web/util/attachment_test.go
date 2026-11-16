package util

import (
	"mime"
	"testing"
)

// attachmentCases returns the names TestAttachment checks and the values it
// wants; FuzzAttachment starts from the same names.
func attachmentCases() []struct{ name, want string } {
	return []struct{ name, want string }{
		{name: "router.pcap", want: `attachment; filename=router.pcap`},
		{name: "a b.pcap", want: `attachment; filename="a b.pcap"`},
		{name: `say "hi".txt`, want: `attachment; filename="say \"hi\".txt"`},
		{name: `back\slash`, want: `attachment; filename="back\\slash"`},
		{name: "phēnix.yml", want: `attachment; filename*=utf-8''ph%C4%93nix.yml`},
		{name: "", want: `attachment; filename=""`},
		{name: "a;b=c.txt", want: `attachment; filename="a;b=c.txt"`},
		// a line break is encoded, so a name cannot add a header
		{name: "a\r\nSet-Cookie: x", want: `attachment; filename*=utf-8''a%0D%0ASet-Cookie%3A%20x`},
	}
}

func TestAttachment(t *testing.T) {
	t.Parallel()

	for _, tc := range attachmentCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := Attachment(tc.name)
			if got != tc.want {
				t.Errorf("Attachment(%q) = %s, want %s", tc.name, got, tc.want)
			}

			// browsers read back the name as given
			if _, params, err := mime.ParseMediaType(got); err != nil || params["filename"] != tc.name {
				t.Errorf("mime.ParseMediaType(Attachment(%q)) filename = %q, %v; want %q", tc.name, params["filename"], err, tc.name)
			}
		})
	}
}
