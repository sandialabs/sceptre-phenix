package builderbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var grapheditor = filepath.Join("..", "public", "grapheditor") //nolint:gochecknoglobals // test fixture

func TestBundleInlinesEveryInclude(t *testing.T) {
	bundle, err := Build(os.DirFS(grapheditor))
	if err != nil {
		t.Fatalf("Build(%s) error = %v", grapheditor, err)
	}

	src := string(bundle)

	if strings.Contains(src, "mxClient.include(mxClient.basePath") {
		t.Error("Build() output loads mxGraph sources one by one, want them inlined")
	}

	for _, name := range []string{"src/js/view/mxGraph.js", "src/js/io/mxEditorCodec.js", "js/Dialogs.js"} {
		if !strings.Contains(src, "// --- "+name+"\n") {
			t.Errorf("Build() output lacks %s, want it inlined", name)
		}
	}

	if got := strings.Count(src, "// --- src/js/"); got != 141 {
		t.Errorf("Build() output has %d mxGraph files, want mxClient.js and its 140 includes", got)
	}
}
