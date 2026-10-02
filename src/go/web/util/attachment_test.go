package util

import (
	"mime"
	"testing"
)

func TestAttachment(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"router.pcap":  `attachment; filename=router.pcap`,
		"a b.pcap":     `attachment; filename="a b.pcap"`,
		`say "hi".txt`: `attachment; filename="say \"hi\".txt"`,
		`back\slash`:   `attachment; filename="back\\slash"`,
		"phēnix.yml":   `attachment; filename*=utf-8''ph%C4%93nix.yml`,
	} {
		got := Attachment(name)
		if got != want {
			t.Errorf("Attachment(%q) = %s, want %s", name, got, want)
		}

		// browsers read back the name as given
		if _, params, err := mime.ParseMediaType(got); err != nil || params["filename"] != name {
			t.Errorf("Attachment(%q) parses as %q, %v", name, params["filename"], err)
		}
	}
}
