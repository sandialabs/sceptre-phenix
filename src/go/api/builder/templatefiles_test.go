package builder

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// templateFileText returns a template file of one collection with a template of
// each name. With an icon, the first template names it and the file carries it.
func templateFileText(collection, icon string, data []byte, names ...string) string {
	var text strings.Builder

	text.WriteString("$schema: " + builder.TemplateFileSchemaURI + "\n")
	text.WriteString("name: " + collection + "\n")
	text.WriteString("description: read by a test\n")
	text.WriteString("templates:\n")

	for i, name := range names {
		text.WriteString("  - name: " + name + "\n")
		text.WriteString("    device:\n")

		if i == 0 && icon != "" {
			text.WriteString("      icon: " + icon + "\n")
		}

		text.WriteString("      spec:\n")
		text.WriteString("        type: VirtualMachine\n")
		text.WriteString("        general:\n")
		text.WriteString("          hostname: " + strings.ToLower(name) + "\n")
	}

	if icon != "" {
		text.WriteString("icons:\n")
		text.WriteString("  " + icon + ":\n")
		text.WriteString("    data: " + base64.StdEncoding.EncodeToString(data) + "\n")
	}

	return text.String()
}

// writeTemplateFile writes a file of the template directory.
func writeTemplateFile(t *testing.T, directory, name, text string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// problemFiles returns the names of the files a problem list names.
func problemFiles(problems []TemplateFileProblem) []string {
	files := make([]string, 0, len(problems))

	for _, problem := range problems {
		files = append(files, problem.File)
	}

	return files
}

// logFields returns the named fields of each log record, printed as text, so
// a test can compare them whatever type the JSON of the record gave them.
func logFields(records []map[string]any, keys ...string) []map[string]string {
	fields := make([]map[string]string, len(records))

	for i, record := range records {
		fields[i] = make(map[string]string, len(keys))
		for _, key := range keys {
			fields[i][key] = fmt.Sprint(record[key])
		}
	}

	return fields
}

// templateNames returns the names of the templates of each collection.
func templateNames(collections []ServerCollection) [][]string {
	names := make([][]string, len(collections))

	for i, collection := range collections {
		names[i] = make([]string, len(collection.Templates))
		for j, template := range collection.Templates {
			names[i][j] = template.Name
		}
	}

	return names
}

// serverIDs returns the ID of each collection, each followed by the IDs of its
// templates.
func serverIDs(collections []ServerCollection) []string {
	var ids []string

	for _, collection := range collections {
		ids = append(ids, collection.ID)
		for _, template := range collection.Templates {
			ids = append(ids, template.ID)
		}
	}

	return ids
}

// TestLoadServerTemplates reads good, bad, ignored and irregular files and a link
// that leaves the directory.
func TestLoadServerTemplates(t *testing.T) {
	h := newHarness(t)
	logs := plogtest.Capture(t)
	directory := t.TempDir()
	outside := t.TempDir()

	writeTemplateFile(t, directory, "b-sandia.yml", templateFileText("Sandia Node Templates", "", nil, "Router", "HMI"))
	writeTemplateFile(t, directory, "a-nlr.json", strings.Join([]string{
		`{"$schema": "` + builder.TemplateFileSchemaURI + `", "name": "NLR Node Templates", "templates": [`,
		`{"name": "PLC", "device": {"spec": {"general": {"hostname": "plc"}}}}]}`,
	}, ""))
	writeTemplateFile(t, directory, "c-bad.yaml", templateFileText("Bad", "", nil, "Twin", "twin"))
	writeTemplateFile(t, directory, "notes.txt", "not a template file")
	writeTemplateFile(t, directory, ".hidden.yaml", templateFileText("Hidden", "", nil, "One"))
	writeTemplateFile(t, outside, "escape.yaml", templateFileText("Escaped", "", nil, "One"))

	if err := os.Symlink(filepath.Join(outside, "escape.yaml"), filepath.Join(directory, "d-link.yaml")); err != nil {
		t.Fatalf("linking: %v", err)
	}

	if err := os.Mkdir(filepath.Join(directory, "e-directory.yaml"), 0o700); err != nil {
		t.Fatalf("making a directory: %v", err)
	}

	if err := syscall.Mkfifo(filepath.Join(directory, "f-pipe.yaml"), 0o600); err != nil {
		t.Fatalf("making a named pipe: %v", err)
	}

	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatalf("making a directory: %v", err)
	}

	writeTemplateFile(t, filepath.Join(directory, "nested"), "deeper.yaml", templateFileText("Deeper", "", nil, "One"))

	problems := h.service.LoadServerTemplates(context.Background(), directory)
	skipped := []string{"c-bad.yaml", "d-link.yaml", "e-directory.yaml", "f-pipe.yaml"}

	// The problems are indexed below.
	if got := problemFiles(problems); !slices.Equal(got, skipped) {
		t.Fatalf("LoadServerTemplates skipped %q, want %q", got, skipped)
	}

	if want := `template name "twin" is also the name of templates[0]`; !strings.Contains(problems[0].Reason, want) {
		t.Errorf("LoadServerTemplates skipped c-bad.yaml for %q, want it to name the duplicate: %s", problems[0].Reason, want)
	}

	// A directory and a pipe are not regular files.
	if diff := cmp.Diff([]string{notRegular, notRegular}, []string{problems[2].Reason, problems[3].Reason}); diff != "" {
		t.Errorf("LoadServerTemplates reasons for e-directory.yaml and f-pipe.yaml mismatch (-want +got):\n%s", diff)
	}

	// Each skipped file is logged once, as a warning with its name and why.
	for _, problem := range problems {
		records := logs.Records(t, func(record map[string]any) bool {
			return record["msg"] == "skipping builder template file" && record["file"] == problem.File
		})

		want := []map[string]string{{"reason": problem.Reason, "level": "WARN"}}
		if diff := cmp.Diff(want, logFields(records, "reason", "level")); diff != "" {
			t.Errorf("log of skipping %s mismatch (-want +got):\n%s", problem.File, diff)
		}
	}

	collections := h.service.ServerCollections()

	// The collections are indexed below.
	if len(collections) != 2 {
		t.Fatalf("ServerCollections() = %+v, want the two good files", collections)
	}

	want := []ServerCollection{
		{Name: "NLR Node Templates", File: "a-nlr.json"},
		{Name: "Sandia Node Templates", Description: "read by a test", File: "b-sandia.yml"},
	}

	if diff := cmp.Diff(want, collections, cmpopts.IgnoreFields(ServerCollection{}, "ID", "Templates")); diff != "" {
		t.Errorf("ServerCollections() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([][]string{{"PLC"}, {"Router", "HMI"}}, templateNames(collections)); diff != "" {
		t.Errorf("ServerCollections() template names mismatch (-want +got):\n%s", diff)
	}

	// Identifiers are derived from the file and the template, and usable as
	// any other.
	ids := serverIDs(collections)

	for _, id := range ids {
		if !ValidID(id) || !strings.HasPrefix(id, "server-") {
			t.Errorf("ServerCollections() identifier %q, want a valid ID that starts with server-", id)
		}
	}

	if unique := slices.Compact(slices.Sorted(slices.Values(ids))); len(unique) != 5 {
		t.Errorf("ServerCollections() identifiers = %q, want 5 that do not repeat", ids)
	}

	// A second start reads the same identifiers.
	again := newHarness(t)
	again.service.LoadServerTemplates(context.Background(), directory)

	if diff := cmp.Diff(ids, serverIDs(again.service.ServerCollections())); diff != "" {
		t.Errorf("ServerCollections() identifiers after a second read mismatch (-want +got):\n%s", diff)
	}

	// What is returned is a copy.
	collections[0].Templates[0].Name = "changed"
	if got := h.service.ServerCollections()[0].Templates[0].Name; got != "PLC" {
		t.Errorf("ServerCollections() template name after changing a returned copy = %q, want PLC", got)
	}
}

func TestLoadServerTemplatesWithoutADirectory(t *testing.T) {
	h := newHarness(t)
	logs := plogtest.Capture(t)
	missing := filepath.Join(t.TempDir(), "builder", "templates")

	if problems := h.service.LoadServerTemplates(context.Background(), missing); len(problems) != 0 {
		t.Errorf("LoadServerTemplates(missing directory) = %v, want no problems", problems)
	}

	// An empty list, not nil.
	if diff := cmp.Diff([]ServerCollection{}, h.service.ServerCollections()); diff != "" {
		t.Errorf("ServerCollections() of a missing directory mismatch (-want +got):\n%s", diff)
	}

	records := logs.Records(t, plogtest.Message("builder template directory does not exist"))

	want := []map[string]string{{"level": "DEBUG", "directory": missing}}
	if diff := cmp.Diff(want, logFields(records, "level", "directory")); diff != "" {
		t.Errorf("log of a missing directory mismatch (-want +got):\n%s", diff)
	}

	// No directory at all reads nothing, and a service that never read one
	// has no collections.
	if problems := h.service.LoadServerTemplates(context.Background(), ""); len(problems) != 0 {
		t.Errorf("LoadServerTemplates(\"\") = %v, want no problems", problems)
	}

	if diff := cmp.Diff([]ServerCollection{}, newHarness(t).service.ServerCollections()); diff != "" {
		t.Errorf("ServerCollections() of a service that read nothing mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadServerTemplatesReadsAtMostSoManyFiles(t *testing.T) {
	h := newHarness(t)
	directory := t.TempDir()

	for i := range MaxServerTemplateFiles + 2 {
		name := string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".yaml"
		writeTemplateFile(t, directory, name, templateFileText("C "+name, "", nil, "One"))
	}

	problems := h.service.LoadServerTemplates(context.Background(), directory)

	if got := len(h.service.ServerCollections()); got != MaxServerTemplateFiles {
		t.Errorf("LoadServerTemplates read %d collections, want %d", got, MaxServerTemplateFiles)
	}

	if len(problems) != 2 {
		t.Fatalf("LoadServerTemplates skipped %d files, want 2", len(problems))
	}

	if problems[0].File != "by.yaml" {
		t.Errorf("LoadServerTemplates first skipped file = %q, want by.yaml", problems[0].File)
	}

	if !strings.Contains(problems[0].Reason, "more than 50 template files") {
		t.Errorf("LoadServerTemplates skipped by.yaml for %q, want it to name the limit of 50", problems[0].Reason)
	}
}

// TestLoadServerTemplatesAddsIcons adds a template file's icon as the server's,
// and keeps a different library icon of that name.
func TestLoadServerTemplatesAddsIcons(t *testing.T) {
	h := newHarness(t)
	logs := plogtest.Capture(t)
	directory := t.TempDir()

	theirs := mustAddIcon(t, h, testActor, "taken", iconPixel(t, 1))

	writeTemplateFile(t, directory, "one.yaml", templateFileText("One", "fresh", iconPixel(t, 2), "PLC"))
	writeTemplateFile(t, directory, "two.yaml", templateFileText("Two", "taken", iconPixel(t, 3), "HMI"))
	writeTemplateFile(t, directory, "three.yaml", templateFileText("Three", "Taken", iconPixel(t, 1), "RTU"))

	if problems := h.service.LoadServerTemplates(context.Background(), directory); len(problems) != 0 {
		t.Errorf("LoadServerTemplates = %v, want no problems", problems)
	}

	fresh := mustGetIcon(t, h, "fresh")
	if fresh.Owner != ServerIconOwner {
		t.Errorf("GetIcon(fresh) owner = %q, want %q, no user", fresh.Owner, ServerIconOwner)
	}

	if got, want := iconBytes(t, fresh), iconPixel(t, 2); !bytes.Equal(got, want) {
		t.Errorf("GetIcon(fresh) PNG = %x, want the file's %x", got, want)
	}

	kept := mustGetIcon(t, h, "taken")
	if diff := iconDiff(*theirs, *kept); diff != "" {
		t.Errorf("GetIcon(taken) after LoadServerTemplates mismatch (-want +got):\n%s", diff)
	}

	conflicts := logs.Records(
		t, plogtest.Message("builder template file icon differs from the icon library's, which is kept"),
	)

	want := []map[string]string{{"file": "two.yaml", "icon": "taken", "owner": testActor}}
	if diff := cmp.Diff(want, logFields(conflicts, "file", "icon", "owner")); diff != "" {
		t.Errorf("logged icon conflicts mismatch (-want +got):\n%s", diff)
	}

	// The same bytes under the name in another case are the library's icon:
	// nothing is added, and nothing is logged.
	added := logs.Records(t, plogtest.Message("added builder template file icon to the icon library"))
	if diff := cmp.Diff([]map[string]string{{"icon": "fresh"}}, logFields(added, "icon")); diff != "" {
		t.Errorf("logged icons added mismatch (-want +got):\n%s", diff)
	}

	// The templates name the icons as the files do, whichever the library
	// draws.
	collections := h.service.ServerCollections()
	if len(collections) < 3 || len(collections[2].Templates) == 0 {
		t.Fatalf("ServerCollections() = %+v, want three collections", collections)
	}

	if got := collections[2].Name; got != "Two" {
		t.Errorf("ServerCollections()[2] name = %q, want Two", got)
	}

	if got := collections[2].Templates[0].Device.Icon; got != "taken" {
		t.Errorf("ServerCollections() Two template icon = %q, want taken", got)
	}
}

// TestServerIconsBelongToNoUser asserts no user, not even one named phenix,
// renames or deletes an icon of the template files.
func TestServerIconsBelongToNoUser(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	directory := t.TempDir()

	writeTemplateFile(t, directory, "one.yaml", templateFileText("One", "fresh", iconPixel(t, 2), "PLC"))
	h.service.LoadServerTemplates(ctx, directory)

	if ServerIconOwner != "" {
		t.Errorf("ServerIconOwner = %q, want no user name", ServerIconOwner)
	}

	// The icon is owned, created and updated by no user name.
	icon := mustGetIcon(t, h, "fresh")
	if icon.Owner != ServerIconOwner {
		t.Errorf("GetIcon(fresh) owner = %q, want %q", icon.Owner, ServerIconOwner)
	}

	if diff := cmp.Diff(iconAuthors{}, authorsOf(icon)); diff != "" {
		t.Errorf("GetIcon(fresh) authors mismatch (-want +got):\n%s", diff)
	}

	for _, caller := range []string{"phenix", testActor} {
		t.Run(caller, func(t *testing.T) {
			if _, err := h.service.RenameIcon(ctx, caller, "fresh", "renamed"); !errors.Is(err, ErrForbidden) {
				t.Errorf("RenameIcon as %s = %v, want ErrForbidden", caller, err)
			}

			if err := h.service.DeleteIcon(ctx, caller, "fresh", iconTag(t, h, "fresh")); !errors.Is(err, ErrForbidden) {
				t.Errorf("DeleteIcon as %s = %v, want ErrForbidden", caller, err)
			}
		})
	}

	// No caller is the server: an empty caller is refused, and no upload
	// adds an icon as the server's.
	if _, err := h.service.RenameIcon(ctx, ServerIconOwner, "fresh", "renamed"); !errors.Is(err, ErrInvalid) {
		t.Errorf("RenameIcon with no caller = %v, want ErrInvalid", err)
	}

	if err := h.service.DeleteIcon(ctx, ServerIconOwner, "fresh", iconTag(t, h, "fresh")); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteIcon with no caller = %v, want ErrInvalid", err)
	}

	if _, _, err := h.service.AddIcon(ctx, ServerIconOwner, "mine", iconPixel(t, 3)); !errors.Is(err, ErrInvalid) {
		t.Errorf("AddIcon with no owner = %v, want ErrInvalid", err)
	}

	// Its name is refused to an upload in words that name the server.
	_, _, err := h.service.AddIcon(ctx, "phenix", "FRESH", iconPixel(t, 3))

	var taken *IconNameTakenError
	if !errors.As(err, &taken) {
		t.Errorf("AddIcon(FRESH) over the server's icon = %v, want an IconNameTakenError", err)
	} else if diff := cmp.Diff(
		`icon name "FRESH" belongs to an icon the server added from its template files. Choose another name`,
		taken.Sentence(),
	); diff != "" {
		t.Errorf("AddIcon(FRESH) over the server's icon Sentence() mismatch (-want +got):\n%s", diff)
	}
}

// plantLibraryIcons writes count icons named icon-0000 on straight to the store.
// icon-0000 and icon-0001 hold iconPixel 0 and 1. The rest share one PNG.
func plantLibraryIcons(t *testing.T, h *testHarness, count int) {
	t.Helper()

	shared := iconPixel(t, 2)

	for i := range count {
		data := shared
		if i < 2 {
			data = iconPixel(t, i)
		}

		name := fmt.Sprintf("icon-%04d", i)
		value := iconRecordValue(t, LibraryIcon{
			Kind: "icon", Name: name, ID: builder.IconID(data), Owner: fmt.Sprintf("user-%03d", i/50),
			Width: 1, Height: 1, Bytes: len(data), Created: memrecord.Time(1), Updated: memrecord.Time(1),
			Aliases: []string{}, Data: base64.StdEncoding.EncodeToString(data), Revision: 0,
		})

		if _, err := h.store.CreateRecord(NamespaceIcons, "name/"+name, value); err != nil {
			t.Fatalf("planting icon %d: %v", i, err)
		}
	}
}

// TestLoadServerTemplatesLeavesRoomForUsers asserts template file icons stop at
// ServerIconReserve below the limit, with one warning, and users can still upload.
func TestLoadServerTemplatesLeavesRoomForUsers(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	directory := t.TempDir()
	limit := MaxIcons - ServerIconReserve

	plantLibraryIcons(t, h, limit-1)

	logs := plogtest.Capture(t)

	writeTemplateFile(t, directory, "a.yaml", templateFileText("A", "first", iconPixel(t, 5001), "PLC"))
	writeTemplateFile(t, directory, "b.yaml", templateFileText("B", "second", iconPixel(t, 5002), "HMI"))
	writeTemplateFile(t, directory, "c.yaml", templateFileText("C", "icon-0000", iconPixel(t, 0), "RTU"))
	writeTemplateFile(t, directory, "d.yaml", templateFileText("D", "icon-0001", iconPixel(t, 5004), "Valve"))

	if problems := h.service.LoadServerTemplates(ctx, directory); len(problems) != 0 {
		t.Errorf("LoadServerTemplates = %v, want no problems", problems)
	}

	if icon := mustGetIcon(t, h, "first"); icon.Owner != ServerIconOwner {
		t.Errorf("GetIcon(first) owner = %q, want %q, the server's", icon.Owner, ServerIconOwner)
	}

	if _, err := h.service.GetIcon(ctx, "second"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIcon(second) = %v, want the icon past the limit skipped", err)
	}

	full := logs.Records(t, plogtest.Message("builder icon library has no room for more template file icons"))

	want := []map[string]string{{"level": "WARN", "skipped": "1", "limit": strconv.Itoa(limit)}}
	if diff := cmp.Diff(want, logFields(full, "level", "skipped", "limit")); diff != "" {
		t.Errorf("log of the full library mismatch (-want +got):\n%s", diff)
	}

	// An icon the library holds with other bytes is a conflict, not a lack
	// of room.
	conflicts := logs.Records(
		t, plogtest.Message("builder template file icon differs from the icon library's, which is kept"),
	)
	if diff := cmp.Diff([]map[string]string{{"file": "d.yaml"}}, logFields(conflicts, "file")); diff != "" {
		t.Errorf("logged icon conflicts mismatch (-want +got):\n%s", diff)
	}

	// Every file's collection is read, whichever icons were added.
	if got := len(h.service.ServerCollections()); got != 4 {
		t.Errorf("ServerCollections() = %d collections, want 4", got)
	}

	// Users can still upload.
	mustAddIcon(t, h, testActor, "after", iconPixel(t, 5005))

	if got := len(mustListIcons(t, h)); got != limit+1 {
		t.Errorf("ListIcons() = %d icons, want %d", got, limit+1)
	}
}

// TestLoadServerTemplatesWhenTheDirectoryIsAFile gives no collections and a
// warning, and no error, for a template directory that is a file.
func TestLoadServerTemplatesWhenTheDirectoryIsAFile(t *testing.T) {
	h := newHarness(t)
	logs := plogtest.Capture(t)
	parent := t.TempDir()
	path := filepath.Join(parent, "templates")

	writeTemplateFile(t, parent, "templates", templateFileText("Plant", "", nil, "PLC"))

	if _, err := ReadTemplateDirectory(path); err == nil {
		t.Error("ReadTemplateDirectory(regular file) error = nil, want an error")
	}

	if problems := h.service.LoadServerTemplates(context.Background(), path); len(problems) != 0 {
		t.Errorf("LoadServerTemplates(regular file) = %v, want no problems", problems)
	}

	// An empty list, not nil.
	if diff := cmp.Diff([]ServerCollection{}, h.service.ServerCollections()); diff != "" {
		t.Errorf("ServerCollections() of a directory that is a file mismatch (-want +got):\n%s", diff)
	}

	records := logs.Records(t, plogtest.Message("builder template directory cannot be read"))

	want := []map[string]string{{"level": "WARN", "directory": path}}
	if diff := cmp.Diff(want, logFields(records, "level", "directory")); diff != "" {
		t.Errorf("log of a directory that is a file mismatch (-want +got):\n%s", diff)
	}
}
