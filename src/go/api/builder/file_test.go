package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// fileMarker is text the files of these tests hold that no error and no log
// line may repeat.
const fileMarker = "MARKER-FROM-THE-FILE"

// documentFiles is a directory Builder files are read from, with a directory
// excluded from it and one outside it.
type documentFiles struct {
	t        *testing.T
	root     string
	excluded string
	outside  string
}

func newDocumentFiles(t *testing.T) *documentFiles {
	t.Helper()

	root := filepath.Join(t.TempDir(), "phenix")
	files := &documentFiles{t: t, root: root, excluded: filepath.Join(root, "mounts"), outside: t.TempDir()}

	for _, directory := range []string{filepath.Join(root, "topologies", "site"), filepath.Join(files.excluded, "vm")} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatalf("creating %s: %v", directory, err)
		}
	}

	return files
}

// path returns the path of the file name in the site directory below root.
func (f *documentFiles) path(name string) string {
	return filepath.Join(f.root, "topologies", "site", name)
}

// write writes content to path and returns path.
func (f *documentFiles) write(path string, content []byte) string {
	f.t.Helper()

	if err := os.WriteFile(path, content, 0o600); err != nil {
		f.t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

// link makes path a symbolic link to target and returns path.
func (f *documentFiles) link(target, path string) string {
	f.t.Helper()

	if err := os.Symlink(target, path); err != nil {
		f.t.Fatalf("linking %s to %s: %v", path, target, err)
	}

	return path
}

// read reads the Builder file at path.
func (f *documentFiles) read(path string) (*DocumentFile, error) {
	return ReadDocumentFile(f.root, []string{f.excluded}, path)
}

// documentYAML returns the document data holds, written as YAML.
func documentYAML(t *testing.T, data []byte) []byte {
	t.Helper()

	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("decoding the document: %v", err)
	}

	text, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding the document as YAML: %v", err)
	}

	return text
}

// TestReadDocumentFile reads a document from a JSON file and a YAML file,
// whatever the extension says, directly and through a symbolic link that
// stays below the root: each gives the document's canonical JSON and its
// digest, which is not the digest of the file's bytes.
func TestReadDocumentFile(t *testing.T) {
	t.Parallel()

	files := newDocumentFiles(t)
	canonical := testDocument(t, "site", 0)
	asYAML := documentYAML(t, canonical)

	var compact bytes.Buffer
	if err := json.Compact(&compact, canonical); err != nil {
		t.Fatalf("compacting the document: %v", err)
	}

	files.write(files.path("site.builder.json"), compact.Bytes())

	for name, path := range map[string]string{
		"JSON":                      files.path("site.builder.json"),
		"canonical JSON":            files.write(files.path("canonical.json"), canonical),
		"YAML":                      files.write(files.path("site.builder.yaml"), asYAML),
		"YAML named .yml":           files.write(files.path("site.yml"), asYAML),
		"YAML named .json":          files.write(files.path("yaml.json"), asYAML),
		"JSON named .yaml":          files.write(files.path("json.yaml"), compact.Bytes()),
		"a link to a file beside":   files.link("site.builder.yaml", files.path("link.yaml")),
		"a link through the parent": files.link(filepath.Join("..", "site", "site.builder.json"), files.path("up.json")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := files.read(path)
			if err != nil {
				t.Fatalf("ReadDocumentFile returned error: %v", err)
			}

			if !bytes.Equal(file.Data, canonical) || file.Digest != digestOf(canonical) {
				t.Fatalf("file = %d bytes with digest %s, want the canonical document with digest %s",
					len(file.Data), file.Digest, digestOf(canonical))
			}

			if file.Document == nil || file.Document.Name != "site" {
				t.Fatalf("document = %+v, want the document named site", file.Document)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the file: %v", err)
			}

			if name != "canonical JSON" && file.Digest == digestOf(raw) {
				t.Fatal("the digest is the digest of the file's bytes")
			}
		})
	}
}

// TestReadDocumentFileRefusals asks for each file that may not be read. The
// error is one of the closed set, says its fixed sentence, and neither it
// nor any log line repeats what the file holds.
func TestReadDocumentFileRefusals(t *testing.T) { //nolint:paralleltest // captures the log
	logs := plogtest.Capture(t)
	files := newDocumentFiles(t)
	valid := testDocument(t, fileMarker, 0)

	secret := files.write(filepath.Join(files.outside, "secret.json"), valid)
	files.write(filepath.Join(files.excluded, "vm", "guest.json"), valid)

	if err := os.Mkdir(files.path("directory.json"), 0o750); err != nil {
		t.Fatalf("creating a directory: %v", err)
	}

	if err := syscall.Mkfifo(files.path("pipe.json"), 0o600); err != nil {
		t.Fatalf("creating a named pipe: %v", err)
	}

	// A document that fails validation with errors that quote its values.
	invalid := bytes.ReplaceAll(testDocument(t, "site", 0), []byte(builder.SchemaURI), []byte(fileMarker))
	if bytes.Equal(invalid, testDocument(t, "site", 0)) {
		t.Fatal("the invalid document is the valid one")
	}

	padded := append(bytes.Clone(valid), bytes.Repeat([]byte(" "), MaxDocumentBytes)...)

	for name, test := range map[string]struct {
		path   string
		reason DocumentFileReason
		// said is the sentence the error is, with %s for the path.
		said string
		is   error
	}{
		"outside the root": {
			path: secret, reason: DocumentFileOutside, is: ErrInvalid,
			said: "Builder file %s is outside " + files.root + ", the directory phenix reads Builder files from.",
		},
		"the root's parent": {
			path:   files.write(filepath.Join(filepath.Dir(files.root), "beside.json"), valid),
			reason: DocumentFileOutside, is: ErrInvalid,
		},
		"a sibling whose name starts with the root's": {
			path: filepath.Dir(files.root) + "/phenix-other/x.json", reason: DocumentFileOutside, is: ErrInvalid,
		},
		"below the excluded directory": {
			path: filepath.Join(files.excluded, "vm", "guest.json"), reason: DocumentFileOutside, is: ErrInvalid,
		},
		"a link to a file outside the root": {
			path: files.link(secret, files.path("escape.json")), reason: DocumentFileUnreadable, is: ErrInvalid,
			said: "Builder file %s cannot be read by phenix.",
		},
		"a link through a directory outside the root": {
			path:   filepath.Join(files.link(files.outside, filepath.Join(files.root, "topologies", "out")), "secret.json"),
			reason: DocumentFileUnreadable, is: ErrInvalid,
		},
		"a relative link that leaves the root": {
			path: files.link(
				filepath.Join("..", "..", "..", "..", filepath.Base(files.outside), "secret.json"), files.path("climb.json"),
			),
			reason: DocumentFileUnreadable, is: ErrInvalid,
		},
		"an absolute link to a file below the root": {
			path:   files.link(files.write(files.path("linked.json"), valid), files.path("absolute.json")),
			reason: DocumentFileUnreadable, is: ErrInvalid,
		},
		"a link to a file below the excluded directory": {
			path:   files.link(filepath.Join("..", "..", "mounts", "vm", "guest.json"), files.path("guest.json")),
			reason: DocumentFileOutside, is: ErrInvalid,
		},
		"a link through the excluded directory": {
			path: filepath.Join(
				files.link(filepath.Join("..", "mounts", "vm"), filepath.Join(files.root, "topologies", "vm")), "guest.json",
			),
			reason: DocumentFileOutside, is: ErrInvalid,
		},
		"a path with ..": {
			path: files.root + "/../../" + filepath.Base(files.outside) + "/secret.json", reason: DocumentFileUnreadable, is: ErrInvalid,
		},
		"a path that is not clean": {
			path: files.root + "//topologies/site/canonical.json", reason: DocumentFileUnreadable, is: ErrInvalid,
		},
		"a relative path": {path: "topologies/site/canonical.json", reason: DocumentFileUnreadable, is: ErrInvalid},
		"another extension": {
			path: files.write(files.path("store.bdb"), valid), reason: DocumentFileUnreadable, is: ErrInvalid,
		},
		"no extension": {
			path: files.write(files.path("document"), valid), reason: DocumentFileUnreadable, is: ErrInvalid,
		},
		"a missing file": {
			path: files.path("missing.yaml"), reason: DocumentFileMissing, is: ErrNotFound,
			said: "Builder file %s does not exist on this phenix server.",
		},
		"a missing directory": {
			path: filepath.Join(files.root, "nowhere", "site.yaml"), reason: DocumentFileMissing, is: ErrNotFound,
		},
		"a file as a directory": {
			path: filepath.Join(files.write(files.path("plain.yaml"), valid), "site.json"), reason: DocumentFileMissing, is: ErrNotFound,
		},
		"a directory": {
			path: files.path("directory.json"), reason: DocumentFileNotRegular, is: ErrInvalid,
			said: "Builder file %s is not a regular file.",
		},
		"a named pipe": {path: files.path("pipe.json"), reason: DocumentFileNotRegular, is: ErrInvalid},
		"a link to a named pipe": {
			path: files.link("pipe.json", files.path("pipe-link.json")), reason: DocumentFileNotRegular, is: ErrInvalid,
		},
		"more than 5 MiB": {
			path: files.write(files.path("large.json"), padded), reason: DocumentFileTooLarge, is: ErrTooLarge,
			said: "Builder file %s is larger than 5 MiB.",
		},
		"a document with another schema": {
			path: files.write(files.path("schema.json"), invalid), reason: DocumentFileInvalid, is: ErrInvalid,
			said: "Builder file %s is not a valid Builder document. Upload it in the Builder to see why.",
		},
		"a document with an unknown field": {
			path:   files.write(files.path("field.json"), bytes.Replace(valid, []byte(`"name"`), []byte(`"`+fileMarker+`": 1, "name"`), 1)),
			reason: DocumentFileInvalid, is: ErrInvalid,
		},
		"a document with invalid values": {
			path: files.write(files.path("values.yaml"),
				[]byte("$schema: "+builder.SchemaURI+"\nrevision: 1\nid: "+fileMarker+"\nname: "+fileMarker+"\nnodes: "+fileMarker+"\n")),
			reason: DocumentFileInvalid, is: ErrInvalid,
		},
		"text that is not a document": {
			path:   files.write(files.path("passwd.yaml"), []byte("root:x:0:0:"+fileMarker+":/root:/bin/sh\n")),
			reason: DocumentFileInvalid, is: ErrInvalid,
		},
		"YAML with an alias": {
			path:   files.write(files.path("alias.yaml"), []byte("a: &x "+fileMarker+"\nb: *x\n")),
			reason: DocumentFileInvalid, is: ErrInvalid,
		},
		"malformed YAML": {
			path: files.write(files.path("malformed.yaml"), []byte("a: [\""+fileMarker+"\n")), reason: DocumentFileInvalid, is: ErrInvalid,
		},
		"an empty file": {path: files.write(files.path("empty.json"), nil), reason: DocumentFileInvalid, is: ErrInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			file, err := files.read(test.path)

			var fileErr *DocumentFileError

			if !errors.As(err, &fileErr) || file != nil {
				t.Fatalf("ReadDocumentFile = %+v, %v; want a DocumentFileError", file, err)
			}

			want := DocumentFileError{Reason: test.reason, Path: test.path, Root: files.root, Topology: ""}
			if *fileErr != want {
				t.Fatalf("error = %+v, want %+v", *fileErr, want)
			}

			if !errors.Is(err, test.is) {
				t.Fatalf("error %q does not match %v", err, test.is)
			}

			if said := strings.ReplaceAll(test.said, "%s", test.path); said != "" && err.Error() != said {
				t.Fatalf("error = %q, want %q", err, said)
			}

			if errors.Unwrap(errors.Unwrap(err)) != nil {
				t.Fatalf("error %q wraps a cause", err)
			}

			if strings.Contains(err.Error(), fileMarker) {
				t.Fatalf("error %q repeats what the file holds", err)
			}
		})
	}

	if logged := logs.String(); strings.Contains(logged, fileMarker) {
		t.Fatalf("the log repeats what a file holds: %s", logged)
	}
}

// TestReadDocumentFileHidesOtherFiles asks for a path through a regular file
// and for the same path through a name nothing has. Both are missing, in the
// same words, so the answer does not say which names below the root are
// files: the last element of a path only has to end as a document's does.
func TestReadDocumentFileHidesOtherFiles(t *testing.T) {
	t.Parallel()

	files := newDocumentFiles(t)
	files.write(files.path("disk.qc2"), []byte(fileMarker))

	said := map[string]string{}

	for _, name := range []string{"disk.qc2", "nodisk.qc2"} {
		path := filepath.Join(files.path(name), "x.yaml")

		var fileErr *DocumentFileError

		_, err := files.read(path)
		if !errors.As(err, &fileErr) || fileErr.Reason != DocumentFileMissing || !errors.Is(err, ErrNotFound) {
			t.Fatalf("below %s: ReadDocumentFile returned %v, want the file missing", name, err)
		}

		said[name] = strings.ReplaceAll(err.Error(), path, "%s")
	}

	if said["disk.qc2"] != said["nodisk.qc2"] {
		t.Fatalf("below a file: %q, below nothing: %q, want the same sentence", said["disk.qc2"], said["nodisk.qc2"])
	}
}

// TestReadDocumentFileAfterAPanic reads a Builder file after the parser
// panicked on another: a panic does not keep later files from being parsed.
func TestReadDocumentFileAfterAPanic(t *testing.T) { //nolint:paralleltest // replaces the parser
	files := newDocumentFiles(t)
	path := files.write(files.path("site.json"), testDocument(t, "site", 0))

	parse := parseDocumentText
	parseDocumentText = func([]byte) (*DocumentFile, error) { panic("the parser failed") }

	t.Cleanup(func() { parseDocumentText = parse })

	func() {
		defer func() {
			if recover() == nil {
				t.Error("ReadDocumentFile did not panic with a parser that does")
			}
		}()

		_, _ = files.read(path)
	}()

	parseDocumentText = parse

	read := make(chan error, 1)

	go func() {
		_, err := files.read(path)
		read <- err
	}()

	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("ReadDocumentFile returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReadDocumentFile still waits for the parser that panicked")
	}
}

// TestReadDocumentFileUnreadable asks for a file the process may not read.
func TestReadDocumentFileUnreadable(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("the superuser reads every file")
	}

	files := newDocumentFiles(t)
	path := files.write(files.path("locked.json"), testDocument(t, "site", 0))

	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("changing the mode of the file: %v", err)
	}

	var fileErr *DocumentFileError

	if _, err := files.read(path); !errors.As(err, &fileErr) || fileErr.Reason != DocumentFileUnreadable {
		t.Fatalf("ReadDocumentFile returned %v, want the file unreadable", err)
	}
}

// TestReadDocumentFileDirectories reads below a root and around excluded
// directories however they are written: with a trailing slash, relative to
// the working directory, or not existing. No root at all holds no file.
func TestReadDocumentFileDirectories(t *testing.T) { //nolint:paralleltest // changes the working directory
	files := newDocumentFiles(t)
	path := files.write(files.path("site.json"), testDocument(t, "site", 0))
	guest := files.write(filepath.Join(files.excluded, "vm", "guest.json"), testDocument(t, "site", 0))

	reason := func(root string, excluded []string, path string) DocumentFileReason {
		t.Helper()

		_, err := ReadDocumentFile(root, excluded, path)
		if err == nil {
			return ""
		}

		var fileErr *DocumentFileError
		if !errors.As(err, &fileErr) {
			t.Fatalf("ReadDocumentFile returned %v, want a DocumentFileError", err)
		}

		return fileErr.Reason
	}

	t.Chdir(filepath.Dir(files.root))

	for name, test := range map[string]struct {
		root     string
		excluded []string
		path     string
		want     DocumentFileReason
	}{
		"a root with a trailing slash":       {root: files.root + "/", path: path},
		"no excluded directory":              {root: files.root, path: guest},
		"an empty excluded directory":        {root: files.root, excluded: []string{""}, path: guest},
		"an excluded directory that is gone": {root: files.root, excluded: []string{files.root + "/gone"}, path: path},
		"an excluded directory with a slash": {
			root: files.root, excluded: []string{files.excluded + "/"}, path: guest, want: DocumentFileOutside,
		},
		"a second excluded directory": {
			root: files.root, excluded: []string{files.root + "/gone", files.excluded}, path: guest, want: DocumentFileOutside,
		},
		"an excluded directory outside the root": {root: files.root, excluded: []string{files.outside}, path: path},
		"a relative root":                        {root: "phenix", path: path},
		"a relative excluded directory": {
			root: "phenix", excluded: []string{"phenix/mounts"}, path: guest, want: DocumentFileOutside,
		},
		"the root itself":         {root: path, path: path, want: DocumentFileOutside},
		"no root":                 {root: "", path: path, want: DocumentFileOutside},
		"a root that is missing":  {root: files.root + "/gone", path: files.root + "/gone/site.json", want: DocumentFileMissing},
		"a root that is a file":   {root: path, path: path + "/site.json", want: DocumentFileUnreadable},
		"the file system's root":  {root: "/", excluded: []string{files.excluded}, path: path},
		"excluded from that root": {root: "/", excluded: []string{files.excluded}, path: guest, want: DocumentFileOutside},
	} {
		if got := reason(test.root, test.excluded, test.path); got != test.want {
			t.Errorf("%s: reason = %q, want %q", name, got, test.want)
		}
	}
}

// TestDocumentFileErrorDigest is the error of a file whose document is not
// the one its topology's reference pins.
func TestDocumentFileErrorDigest(t *testing.T) {
	t.Parallel()

	err := error(&DocumentFileError{Reason: DocumentFileDigest, Path: "/phenix/site.yaml", Root: "", Topology: "site"})

	if want := "Builder file /phenix/site.yaml does not match the digest topology site records for it."; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}

	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error %q does not match ErrInvalid", err)
	}
}

// TestParseDocumentText decodes the text of a file its caller supplied: the
// error then says what is wrong with it.
func TestParseDocumentText(t *testing.T) { //nolint:paralleltest // sets the environment
	canonical := testDocument(t, "site", 0)

	for name, text := range map[string][]byte{"JSON": canonical, "YAML": documentYAML(t, canonical)} {
		file, err := ParseDocumentText(text)
		if err != nil {
			t.Fatalf("%s: ParseDocumentText returned error: %v", name, err)
		}

		if !bytes.Equal(file.Data, canonical) || file.Digest != digestOf(canonical) || file.Document.Name != "site" {
			t.Fatalf("%s: file = %d bytes with digest %s, want the canonical document", name, len(file.Data), file.Digest)
		}
	}

	// A name a config file would have expanded stays as it is written.
	t.Setenv("BUILDER_FILE_TEST", "expanded")

	file, err := ParseDocumentText(bytes.Replace(canonical, []byte(`"site"`), []byte(`"${BUILDER_FILE_TEST}"`), 1))
	if err != nil || file.Document.Name != "${BUILDER_FILE_TEST}" {
		t.Fatalf("ParseDocumentText = %+v, %v; want the name kept as it is written", file, err)
	}

	for name, test := range map[string]struct {
		text   []byte
		is     error
		reason string
	}{
		"no text": {text: nil, is: ErrInvalid, reason: "must not be empty"},
		"another schema": {
			text: bytes.ReplaceAll(canonical, []byte(builder.SchemaURI), []byte("other")), is: ErrInvalid, reason: `"other"`,
		},
		"an alias": {text: []byte("a: &x 1\nb: *x\n"), is: builder.ErrUnsupportedYAML, reason: "line 1: an anchor"},
		"too large": {
			text: append(bytes.Clone(canonical), bytes.Repeat([]byte(" "), MaxDocumentBytes)...), is: ErrTooLarge, reason: "limit is",
		},
	} {
		file, err := ParseDocumentText(test.text)
		if file != nil || !errors.Is(err, test.is) || !strings.Contains(err.Error(), test.reason) {
			t.Errorf("%s: ParseDocumentText = %+v, %v; want an error matching %v that says %q", name, file, err, test.is, test.reason)
		}
	}
}

// TestLoadDocumentFile reads a document from a file the caller chose, where
// ever it is: as JSON and as YAML it is the same document, and a "${NAME}"
// in it is not expanded.
func TestLoadDocumentFile(t *testing.T) { //nolint:paralleltest // sets the environment
	t.Setenv("BUILDER_FILE_TEST", "expanded")

	directory := t.TempDir()
	canonical := testDocument(t, "site ${BUILDER_FILE_TEST}", 0)

	write := func(name string, content []byte) string {
		path := filepath.Join(directory, name)

		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		return path
	}

	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(write("site.json", canonical), link); err != nil {
		t.Fatalf("linking %s: %v", link, err)
	}

	for name, path := range map[string]string{
		"JSON":             filepath.Join(directory, "site.json"),
		"YAML":             write("site.yaml", documentYAML(t, canonical)),
		"no extension":     write("site", documentYAML(t, canonical)),
		"a symbolic link":  link,
		"a relative path":  mustRelative(t, filepath.Join(directory, "site.json")),
		"YAML named .json": write("yaml.json", documentYAML(t, canonical)),
	} {
		file, err := LoadDocumentFile(path)
		if err != nil {
			t.Fatalf("%s: LoadDocumentFile returned error: %s", name, fmtErr(err))
		}

		if !bytes.Equal(file.Data, canonical) || file.Digest != digestOf(canonical) ||
			file.Document.Name != "site ${BUILDER_FILE_TEST}" {
			t.Errorf("%s: document %q with digest %s, want the file's, unexpanded, with digest %s",
				name, file.Document.Name, file.Digest, digestOf(canonical))
		}
	}
}

// mustRelative returns path relative to the working directory.
func mustRelative(t *testing.T, path string) string {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting the working directory: %v", err)
	}

	// The temporary directory may be named through a symbolic link.
	if resolved, err := filepath.EvalSymlinks(directory); err == nil {
		directory = resolved
	}

	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	relative, err := filepath.Rel(directory, path)
	if err != nil {
		t.Fatalf("making %s relative: %v", path, err)
	}

	return relative
}

// TestLoadDocumentFileRefusals asks for files that hold no usable document.
// The error says what is wrong with the file, for the caller who chose it.
func TestLoadDocumentFileRefusals(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	valid := testDocument(t, "site", 0)

	write := func(name string, content []byte) string {
		path := filepath.Join(directory, name)

		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		return path
	}

	pipe := filepath.Join(directory, "pipe.json")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("creating a named pipe: %v", err)
	}

	for name, test := range map[string]struct {
		path string
		is   error
		says string
	}{
		"a missing file": {path: filepath.Join(directory, "missing.json"), is: os.ErrNotExist, says: "missing.json"},
		"a directory":    {path: directory, is: ErrInvalid, says: "is not a regular file"},
		// Opening it for reading would wait for a writer.
		"a named pipe":  {path: pipe, is: ErrInvalid, says: "is not a regular file"},
		"an empty file": {path: write("empty.json", nil), is: ErrInvalid, says: "must not be empty"},
		"a file that is too large": {
			path: write("large.json", append(bytes.Clone(valid), bytes.Repeat([]byte(" "), MaxDocumentBytes)...)),
			is:   ErrTooLarge,
		},
		"a config": {
			path: write("topology.yaml", []byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: x\n")),
			is:   ErrInvalid, says: `unknown field "apiVersion"`,
		},
		"a document that is not valid": {
			path: write("invalid.json", bytes.ReplaceAll(valid, []byte(builder.SchemaURI), []byte("https://example.com/schema"))),
			is:   builder.ErrUnsupportedSchema, says: "https://example.com/schema",
		},
		"YAML a document may not use": {
			path: write("alias.yaml", []byte("name: &n site\nid: *n\n")), is: builder.ErrUnsupportedYAML, says: "anchor",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := LoadDocumentFile(test.path)
			if file != nil || !errors.Is(err, test.is) || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("LoadDocumentFile = %v, %s, want an error matching %v that says %q", file, fmtErr(err), test.is, test.says)
			}
		})
	}
}

// TestDocumentPathServed tells the paths [ReadDocumentFile] reads at all
// from those it refuses as outside, without looking for a file.
func TestDocumentPathServed(t *testing.T) {
	t.Parallel()

	files := newDocumentFiles(t)

	// A link into the excluded directory is found through a file that exists.
	inside := files.link(filepath.Join(files.excluded, "vm"), files.path("guest"))
	files.write(filepath.Join(files.excluded, "vm", "site.yaml"), testDocument(t, "site", 0))

	for path, want := range map[string]bool{
		files.path("site.yaml"):                              true,
		files.path("missing.yaml"):                           true,
		filepath.Join(files.root, "site.yaml"):               true,
		filepath.Join(files.outside, "site.yaml"):            false,
		filepath.Join(files.excluded, "vm", "site.yaml"):     false,
		filepath.Join(inside, "site.yaml"):                   false,
		files.root:                                           false,
		filepath.Dir(files.root) + "/phenix-other/site.yaml": false,
	} {
		if got := DocumentPathServed(files.root, []string{files.excluded}, path); got != want {
			t.Errorf("DocumentPathServed(%s) = %t, want %t", path, got, want)
		}
	}

	if DocumentPathServed("", nil, files.path("site.yaml")) {
		t.Error("a path is served although no directory is")
	}
}
