package image

import (
	"bytes"
	"phenix/tmpl"
	v1 "phenix/types/version/v1"
	"strings"
	"testing"
)

func TestImageTemplate(t *testing.T) {
	img := v1.Image{
		Release:     "noble",
		Size:        "10G",
		Packages:    []string{"wireshark"},
		VerboseLogs: true,
	}

	if err := SetDefaults(&img); err != nil {
		t.Log(err)
		t.FailNow()
	}

	var buf bytes.Buffer

	if err := tmpl.GenerateFromTemplate("vmdb.tmpl", img, &buf); err != nil {
		t.Log(err)
		t.FailNow()
	}

	if !strings.Contains(buf.String(), `- wireshark`) {
		t.Log("missing packages in options")
		t.FailNow()
	}
}
