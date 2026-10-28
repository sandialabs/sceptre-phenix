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

	"github.com/google/go-cmp/cmp"
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

// TestReadDocumentFile reads JSON and YAML files, whatever the extension, also
// through a link below the root: each gives the canonical JSON and its digest.
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
				t.Fatalf("ReadDocumentFile(%s) returned error: %v", path, err)
			}

			if diff := cmp.Diff(string(canonical), string(file.Data)); diff != "" {
				t.Errorf("ReadDocumentFile(%s).Data mismatch with the canonical document (-want +got):\n%s", path, diff)
			}

			if want := digestOf(canonical); file.Digest != want {
				t.Errorf("ReadDocumentFile(%s).Digest = %s, want the canonical document digest %s", path, file.Digest, want)
			}

			if file.Document == nil {
				t.Errorf("ReadDocumentFile(%s).Document = nil, want the document named site", path)
			} else if got := file.Document.Metadata.Name; got != "site" {
				t.Errorf("ReadDocumentFile(%s).Document name = %q, want site", path, got)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the file: %v", err)
			}

			if name != "canonical JSON" && file.Digest == digestOf(raw) {
				t.Errorf(
					"ReadDocumentFile(%s).Digest = %s, the digest of the file bytes, want the canonical document digest",
					path,
					file.Digest,
				)
			}
		})
	}
}

// TestReadDocumentFileRefusals asks for each file that may not be read: the error
// says its fixed sentence, and no error or log line repeats the file's text.
func TestReadDocumentFileRefusals(t *testing.T) {
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
		"the parent of the root": {
			path:   files.write(filepath.Join(filepath.Dir(files.root), "beside.json"), valid),
			reason: DocumentFileOutside, is: ErrInvalid,
		},
		"a sibling whose name starts with the root name": {
			path: filepath.Dir(files.root) + "/phenix-other/x.json", reason: DocumentFileOutside, is: ErrInvalid,
		},
		"below the excluded directory": {
			path: filepath.Join(files.excluded, "vm", "guest.json"), reason: DocumentFileOutside, is: ErrInvalid,
		},
		"a link to a file outside the root": {
			path: files.link(secret, files.path("escape.json")), reason: DocumentFileUnreadable, is: ErrInvalid,
			said: "phenix cannot read Builder file %s.",
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

			if file != nil {
				t.Errorf("ReadDocumentFile(%s) = %+v, want nil", test.path, file)
			}

			if !errors.As(err, &fileErr) {
				t.Fatalf("ReadDocumentFile(%s) error = %v, want a *DocumentFileError", test.path, err)
			}

			want := DocumentFileError{Reason: test.reason, Path: test.path, Root: files.root, Topology: ""}
			if diff := cmp.Diff(want, *fileErr); diff != "" {
				t.Errorf("ReadDocumentFile(%s) error mismatch (-want +got):\n%s", test.path, diff)
			}

			if !errors.Is(err, test.is) {
				t.Errorf("ReadDocumentFile(%s) error = %q, want %v", test.path, err, test.is)
			}

			if said := strings.ReplaceAll(test.said, "%s", test.path); said != "" && err.Error() != said {
				t.Errorf("ReadDocumentFile(%s) error = %q, want %q", test.path, err, said)
			}

			if cause := errors.Unwrap(errors.Unwrap(err)); cause != nil {
				t.Errorf("ReadDocumentFile(%s) error %q wraps the cause %v, want none", test.path, err, cause)
			}

			if strings.Contains(err.Error(), fileMarker) {
				t.Errorf("ReadDocumentFile(%s) error = %q, want it not to repeat what the file holds", test.path, err)
			}
		})
	}

	if logged := logs.String(); strings.Contains(logged, fileMarker) {
		t.Errorf("log after ReadDocumentFile refusals = %s, want it not to repeat what a file holds", logged)
	}
}

// TestReadDocumentFileHidesOtherFiles asks for a path through a regular file and
// through a missing name: both are missing in the same words.
func TestReadDocumentFileHidesOtherFiles(t *testing.T) {
	t.Parallel()

	files := newDocumentFiles(t)
	files.write(files.path("disk.qc2"), []byte(fileMarker))

	said := map[string]string{}

	for _, name := range []string{"disk.qc2", "nodisk.qc2"} {
		path := filepath.Join(files.path(name), "x.yaml")

		var fileErr *DocumentFileError

		_, err := files.read(path)
		if !errors.As(err, &fileErr) {
			t.Errorf("ReadDocumentFile(%s) error = %v, want a *DocumentFileError", path, err)

			continue
		}

		if fileErr.Reason != DocumentFileMissing {
			t.Errorf("ReadDocumentFile(%s) reason = %q, want %q", path, fileErr.Reason, DocumentFileMissing)
		}

		if !errors.Is(err, ErrNotFound) {
			t.Errorf("ReadDocumentFile(%s) error = %v, want ErrNotFound", path, err)
		}

		said[name] = strings.ReplaceAll(err.Error(), path, "%s")
	}

	if said["disk.qc2"] != said["nodisk.qc2"] {
		t.Errorf("ReadDocumentFile below a file = %q, want %q as below nothing", said["disk.qc2"], said["nodisk.qc2"])
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

	if _, err := files.read(path); !errors.As(err, &fileErr) {
		t.Errorf("ReadDocumentFile(file of mode 0) error = %v, want a *DocumentFileError", err)
	} else if fileErr.Reason != DocumentFileUnreadable {
		t.Errorf("ReadDocumentFile(file of mode 0) reason = %q, want %q", fileErr.Reason, DocumentFileUnreadable)
	}
}

// TestReadDocumentFileDirectories reads below roots and excluded directories with
// a trailing slash, relative, or missing. No root holds no file.
func TestReadDocumentFileDirectories(t *testing.T) {
	files := newDocumentFiles(t)
	path := files.write(files.path("site.json"), testDocument(t, "site", 0))
	guest := files.write(filepath.Join(files.excluded, "vm", "guest.json"), testDocument(t, "site", 0))

	// reason returns the reason ReadDocumentFile refuses path, or the error it
	// returns that is not a *DocumentFileError.
	reason := func(root string, excluded []string, path string) (DocumentFileReason, error) {
		_, err := ReadDocumentFile(root, excluded, path)
		if err == nil {
			return "", nil
		}

		var fileErr *DocumentFileError
		if !errors.As(err, &fileErr) {
			return "", err
		}

		return fileErr.Reason, nil
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
		"the file system root":    {root: "/", excluded: []string{files.excluded}, path: path},
		"excluded from that root": {root: "/", excluded: []string{files.excluded}, path: guest, want: DocumentFileOutside},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := reason(test.root, test.excluded, test.path)
			if err != nil {
				t.Fatalf("ReadDocumentFile(%q, %q, %q) error = %v, want nil or a *DocumentFileError",
					test.root, test.excluded, test.path, err)
			}

			if got != test.want {
				t.Errorf("ReadDocumentFile(%q, %q, %q) reason = %q, want %q", test.root, test.excluded, test.path, got, test.want)
			}
		})
	}
}

// TestDocumentFileErrorDigest is the error of a file whose document is not
// the one its topology's reference pins.
func TestDocumentFileErrorDigest(t *testing.T) {
	t.Parallel()

	err := error(&DocumentFileError{Reason: DocumentFileDigest, Path: "/phenix/site.yaml", Root: "", Topology: "site"})

	if want := "Builder file /phenix/site.yaml does not match the digest topology site records for it."; err.Error() != want {
		t.Errorf("DocumentFileError{Reason: DocumentFileDigest}.Error() = %q, want %q", err, want)
	}

	if !errors.Is(err, ErrInvalid) {
		t.Errorf("DocumentFileError{Reason: DocumentFileDigest} = %q, want it to match ErrInvalid", err)
	}
}

// TestParseDocumentText decodes the text of a file its caller supplied: the
// error then says what is wrong with it.
func TestParseDocumentText(t *testing.T) {
	canonical := testDocument(t, "site", 0)

	for name, text := range map[string][]byte{"JSON": canonical, "YAML": documentYAML(t, canonical)} {
		t.Run(name, func(t *testing.T) {
			file, err := ParseDocumentText(text)
			if err != nil {
				t.Fatalf("ParseDocumentText(%s) returned error: %v", name, err)
			}

			if diff := cmp.Diff(string(canonical), string(file.Data)); diff != "" {
				t.Errorf("ParseDocumentText(%s).Data mismatch with the canonical document (-want +got):\n%s", name, diff)
			}

			if want := digestOf(canonical); file.Digest != want {
				t.Errorf("ParseDocumentText(%s).Digest = %s, want the canonical document digest %s", name, file.Digest, want)
			}

			if got := file.Document.Metadata.Name; got != "site" {
				t.Errorf("ParseDocumentText(%s) document name = %q, want site", name, got)
			}
		})
	}

	// A name a config file would have expanded stays as it is written.
	t.Setenv("BUILDER_FILE_TEST", "expanded")

	file, err := ParseDocumentText(bytes.Replace(canonical, []byte(`"site"`), []byte(`"${BUILDER_FILE_TEST}"`), 1))
	if err != nil {
		t.Fatalf("ParseDocumentText(document named ${BUILDER_FILE_TEST}) returned error: %v", err)
	}

	if got := file.Document.Metadata.Name; got != "${BUILDER_FILE_TEST}" {
		t.Errorf("ParseDocumentText(document named ${BUILDER_FILE_TEST}) name = %q, want it kept as it is written", got)
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
		"an alias": {text: []byte("a: &x 1\nb: *x\n"), is: builder.ErrUnsupportedYAML, reason: "line 1: a Builder document cannot have an anchor"},
		"too large": {
			text: append(bytes.Clone(canonical), bytes.Repeat([]byte(" "), MaxDocumentBytes)...), is: ErrTooLarge, reason: "limit is",
		},
	} {
		t.Run(name, func(t *testing.T) {
			file, err := ParseDocumentText(test.text)
			if file != nil {
				t.Errorf("ParseDocumentText(%s) = %+v, want nil", name, file)
			}

			if !errors.Is(err, test.is) {
				t.Errorf("ParseDocumentText(%s) error = %v, want %v", name, err, test.is)
			}

			if err != nil && !strings.Contains(err.Error(), test.reason) {
				t.Errorf("ParseDocumentText(%s) error = %q, want it to say %q", name, err, test.reason)
			}
		})
	}
}
