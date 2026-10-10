package builder

import (
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

	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// templateFileText returns the text of a template file holding a collection
// of the given name with one template of each of the given names. With an
// icon name, the first template names that icon and the file carries it.
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

func TestMaxTemplateFileTemplatesIsTheCollectionLimit(t *testing.T) {
	if builder.MaxTemplateFileTemplates != MaxCollectionTemplates {
		t.Fatalf(
			"a template file holds at most %d templates, and a collection %d: one file is one collection",
			builder.MaxTemplateFileTemplates, MaxCollectionTemplates,
		)
	}
}

// TestLoadServerTemplates reads a directory with two good files, one bad
// one, files the server leaves alone, a file that is no regular file and a
// symbolic link that leaves the directory.
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

	if got := problemFiles(problems); !slices.Equal(got, skipped) {
		t.Fatalf("skipped %q, want %q", got, skipped)
	}

	if !strings.Contains(problems[0].Reason, `template name "twin" is also the name of templates[0]`) {
		t.Errorf("the bad file was skipped for %q, want the duplicate named", problems[0].Reason)
	}

	if problems[2].Reason != notRegular || problems[3].Reason != notRegular {
		t.Errorf("a directory and a pipe were skipped for %q and %q", problems[2].Reason, problems[3].Reason)
	}

	// Each skipped file is logged with its name and why.
	for _, problem := range problems {
		records := logs.Records(t, func(record map[string]any) bool {
			return record["msg"] == "skipping builder template file" && record["file"] == problem.File
		})

		if len(records) != 1 || records[0]["reason"] != problem.Reason || records[0]["level"] != "WARN" {
			t.Errorf("the log of %s = %v, want one warning with its reason", problem.File, records)
		}
	}

	collections := h.service.ServerCollections()

	if len(collections) != 2 {
		t.Fatalf("collections = %+v, want the two good files", collections)
	}

	nlr, sandia := collections[0], collections[1]

	if nlr.Name != "NLR Node Templates" || nlr.File != "a-nlr.json" || len(nlr.Templates) != 1 ||
		nlr.Templates[0].Name != "PLC" {
		t.Errorf("first collection = %+v, want the NLR file's", nlr)
	}

	if sandia.Name != "Sandia Node Templates" || sandia.Description != "read by a test" || len(sandia.Templates) != 2 {
		t.Errorf("second collection = %+v, want the Sandia file's", sandia)
	}

	// Identifiers are derived from the file and the template, and usable as
	// any other.
	templates := slices.Concat(nlr.Templates, sandia.Templates)
	ids := make([]string, 0, len(templates)+2)
	ids = append(ids, nlr.ID, sandia.ID)

	for _, template := range templates {
		ids = append(ids, template.ID)
	}

	for _, id := range ids {
		if !ValidID(id) || !strings.HasPrefix(id, "server-") {
			t.Errorf("identifier %q is not a server identifier", id)
		}
	}

	slices.Sort(ids)

	if len(slices.Compact(ids)) != 5 {
		t.Errorf("identifiers repeat: %q", ids)
	}

	// A second start reads the same identifiers.
	again := newHarness(t)
	again.service.LoadServerTemplates(context.Background(), directory)

	reread := again.service.ServerCollections()
	if reread[1].ID != sandia.ID || reread[1].Templates[1].ID != sandia.Templates[1].ID {
		t.Errorf("a second read gave %+v, want the same identifiers as %+v", reread[1], sandia)
	}

	// What is returned is a copy.
	collections[0].Templates[0].Name = "changed"
	if h.service.ServerCollections()[0].Templates[0].Name != "PLC" {
		t.Error("ServerCollections returned the service's own list")
	}
}

func TestLoadServerTemplatesWithoutADirectory(t *testing.T) {
	h := newHarness(t)
	logs := plogtest.Capture(t)
	missing := filepath.Join(t.TempDir(), "builder", "templates")

	if problems := h.service.LoadServerTemplates(context.Background(), missing); len(problems) != 0 {
		t.Fatalf("a missing directory gave problems %v", problems)
	}

	if got := h.service.ServerCollections(); len(got) != 0 || got == nil {
		t.Fatalf("collections = %#v, want an empty list", got)
	}

	records := logs.Records(t, plogtest.Message("builder template directory does not exist"))
	if len(records) != 1 || records[0]["level"] != "DEBUG" || records[0]["directory"] != missing {
		t.Fatalf("the log of a missing directory = %v, want one debug record naming it", records)
	}

	// No directory at all reads nothing, and a service that never read one
	// has no collections.
	if problems := h.service.LoadServerTemplates(context.Background(), ""); len(problems) != 0 {
		t.Fatalf("no directory gave problems %v", problems)
	}

	if got := newHarness(t).service.ServerCollections(); len(got) != 0 || got == nil {
		t.Fatalf("collections of a service that read nothing = %#v, want an empty list", got)
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

	if len(h.service.ServerCollections()) != MaxServerTemplateFiles || len(problems) != 2 {
		t.Fatalf("read %d collections and skipped %d files, want %d and 2",
			len(h.service.ServerCollections()), len(problems), MaxServerTemplateFiles)
	}

	if problems[0].File != "by.yaml" || !strings.Contains(problems[0].Reason, "more than 50 template files") {
		t.Fatalf("first skipped file = %+v", problems[0])
	}
}

// TestLoadServerTemplatesAddsIcons asserts an icon a template file carries
// is added to the icon library as the server's when the library lacks it,
// and that the library's own icon of that name is kept when it differs.
func TestLoadServerTemplatesAddsIcons(t *testing.T) {
	h := newHarness(t)
	logs := plogtest.Capture(t)
	directory := t.TempDir()

	theirs := mustAddIcon(t, h, testOwner, "taken", iconPixel(t, 1))

	// An account named phenix that uploaded as many icons as one user may
	// does not keep the server from adding those of its files, and is not
	// counted them.
	for i := range MaxLibraryIcons {
		name := "uploaded-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		mustAddIcon(t, h, "phenix", name, iconPixel(t, 100+i))
	}

	writeTemplateFile(t, directory, "one.yaml", templateFileText("One", "fresh", iconPixel(t, 2), "PLC"))
	writeTemplateFile(t, directory, "two.yaml", templateFileText("Two", "taken", iconPixel(t, 3), "HMI"))
	writeTemplateFile(t, directory, "three.yaml", templateFileText("Three", "Taken", iconPixel(t, 1), "RTU"))

	if problems := h.service.LoadServerTemplates(context.Background(), directory); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}

	fresh := mustGetIcon(t, h, "fresh")
	if fresh.Owner != ServerIconOwner || string(iconBytes(t, fresh)) != string(iconPixel(t, 2)) {
		t.Errorf("the new icon = %+v, want the file's, owned by no user", fresh)
	}

	if usage := IconUsage(mustListIcons(t, h), "phenix"); usage.Icons != MaxLibraryIcons {
		t.Errorf("the account named phenix is counted %d icons, want its own %d", usage.Icons, MaxLibraryIcons)
	}

	kept := mustGetIcon(t, h, "taken")
	if !sameIcon(*kept, *theirs) {
		t.Errorf("the library's icon became %+v, want %+v kept", kept, theirs)
	}

	conflicts := logs.Records(
		t, plogtest.Message("builder template file icon differs from the icon library's, which is kept"),
	)
	if len(conflicts) != 1 || conflicts[0]["file"] != "two.yaml" || conflicts[0]["icon"] != "taken" ||
		conflicts[0]["owner"] != testOwner {
		t.Errorf("conflicts logged = %v, want the one of two.yaml", conflicts)
	}

	// The same bytes under the name in another case are the library's icon:
	// nothing is added, and nothing is logged.
	added := logs.Records(t, plogtest.Message("added builder template file icon to the icon library"))
	if len(added) != 1 || added[0]["icon"] != "fresh" {
		t.Errorf("icons added = %v, want only fresh", added)
	}

	// The templates name the icons as the files do, whichever the library
	// draws.
	collections := h.service.ServerCollections()
	if collections[2].Name != "Two" || collections[2].Templates[0].Device.Icon != "taken" {
		t.Errorf("collections = %+v", collections)
	}
}

// TestServerIconsBelongToNoUser asserts an icon the server added from a
// template file is no account's, whatever the account is named: an account
// named phenix may neither rename nor delete it, and only a holder of the
// builder-icons permissions (anyOwner) may.
func TestServerIconsBelongToNoUser(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	directory := t.TempDir()

	writeTemplateFile(t, directory, "one.yaml", templateFileText("One", "fresh", iconPixel(t, 2), "PLC"))
	h.service.LoadServerTemplates(ctx, directory)

	if icon := mustGetIcon(t, h, "fresh"); icon.Owner != ServerIconOwner || ServerIconOwner != "" {
		t.Fatalf("the file's icon = %+v, want it owned by no user name", icon)
	}

	for _, caller := range []string{"phenix", testOwner} {
		if _, err := h.service.RenameIcon(ctx, caller, "fresh", "renamed", false); !errors.Is(err, ErrForbidden) {
			t.Errorf("RenameIcon as %s = %v, want ErrForbidden", caller, err)
		} else if !strings.Contains(err.Error(), `icon "fresh" of the server`) {
			t.Errorf("RenameIcon as %s = %v, want it to name the server", caller, err)
		}

		if err := h.service.DeleteIcon(ctx, caller, "fresh", false); !errors.Is(err, ErrForbidden) {
			t.Errorf("DeleteIcon as %s = %v, want ErrForbidden", caller, err)
		}
	}

	// No caller is the server: an empty caller is refused, and no upload
	// adds an icon as the server's.
	if _, err := h.service.RenameIcon(ctx, ServerIconOwner, "fresh", "renamed", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("RenameIcon with no caller = %v, want ErrInvalid", err)
	}

	if err := h.service.DeleteIcon(ctx, ServerIconOwner, "fresh", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeleteIcon with no caller = %v, want ErrInvalid", err)
	}

	if _, _, err := h.service.AddIcon(ctx, ServerIconOwner, "mine", iconPixel(t, 3)); !errors.Is(err, ErrInvalid) {
		t.Errorf("AddIcon with no owner = %v, want ErrInvalid", err)
	}

	// Its name is refused to an upload in words that name the server.
	_, _, err := h.service.AddIcon(ctx, "phenix", "FRESH", iconPixel(t, 3))

	var taken *IconNameTakenError
	if !errors.As(err, &taken) ||
		taken.Sentence() != `icon name "FRESH" is taken by an icon the server added from its template files; choose another name` {
		t.Errorf("AddIcon over the server's icon = %v, want its name taken by the server's icon", err)
	}

	renamed, err := h.service.RenameIcon(ctx, "phenix", "fresh", "renamed", true)
	if err != nil || renamed.Owner != ServerIconOwner || renamed.Name != "renamed" {
		t.Fatalf("RenameIcon with anyOwner = %+v, %v; want it renamed, still the server's", renamed, err)
	}

	if err := h.service.DeleteIcon(ctx, "phenix", "renamed", true); err != nil {
		t.Fatalf("DeleteIcon with anyOwner = %v", err)
	}
}

// plantLibraryIcons fills the icon library with count icons of users, named
// icon-0000 on, each of the pixel of its number, written to the store as
// records rather than uploaded.
func plantLibraryIcons(t *testing.T, h *testHarness, count int) {
	t.Helper()

	for i := range count {
		data := iconPixel(t, i)
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

// TestLoadServerTemplatesLeavesRoomForUsers asserts the icons of the
// template files fill the icon library only up to what it leaves to users
// (ServerIconReserve): one icon goes into the last place, the next is
// skipped with one warning, an icon the library holds as it is is still
// found, and users can still upload.
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
		t.Fatalf("problems = %v, want none", problems)
	}

	if icon := mustGetIcon(t, h, "first"); icon.Owner != ServerIconOwner {
		t.Errorf("the first file's icon = %+v, want it added as the server's", icon)
	}

	if _, err := h.service.GetIcon(ctx, "second"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIcon(second) = %v, want the icon past the limit skipped", err)
	}

	full := logs.Records(t, plogtest.Message("builder icon library has no room for more template file icons"))
	if len(full) != 1 || full[0]["level"] != "WARN" || fmt.Sprint(full[0]["skipped"]) != "1" ||
		fmt.Sprint(full[0]["limit"]) != strconv.Itoa(limit) {
		t.Errorf("the log of the full library = %v, want one warning with one icon skipped", full)
	}

	// An icon the library holds with other bytes is a conflict, not a lack
	// of room.
	conflicts := logs.Records(
		t, plogtest.Message("builder template file icon differs from the icon library's, which is kept"),
	)
	if len(conflicts) != 1 || conflicts[0]["file"] != "d.yaml" {
		t.Errorf("conflicts logged = %v, want the one of d.yaml", conflicts)
	}

	// Every file's collection is read, whichever icons were added.
	if got := len(h.service.ServerCollections()); got != 4 {
		t.Errorf("read %d collections, want 4", got)
	}

	// Users can still upload.
	mustAddIcon(t, h, testOwner, "after", iconPixel(t, 5005))

	if got := len(mustListIcons(t, h)); got != limit+1 {
		t.Errorf("the library holds %d icons, want %d", got, limit+1)
	}
}

// TestLoadServerTemplatesWhenTheDirectoryIsAFile asserts a template
// directory path that holds a regular file, which cannot be opened as a
// directory, gives the server no collections and a warning, and no error.
func TestLoadServerTemplatesWhenTheDirectoryIsAFile(t *testing.T) {
	h := newHarness(t)
	logs := plogtest.Capture(t)
	parent := t.TempDir()
	path := filepath.Join(parent, "templates")

	writeTemplateFile(t, parent, "templates", templateFileText("Plant", "", nil, "PLC"))

	if _, err := ReadTemplateDirectory(path); err == nil {
		t.Fatal("ReadTemplateDirectory opened a regular file as a directory")
	}

	if problems := h.service.LoadServerTemplates(context.Background(), path); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}

	if got := h.service.ServerCollections(); len(got) != 0 || got == nil {
		t.Fatalf("collections = %#v, want an empty list", got)
	}

	records := logs.Records(t, plogtest.Message("builder template directory cannot be read"))
	if len(records) != 1 || records[0]["level"] != "WARN" || records[0]["directory"] != path {
		t.Fatalf("the log of a directory that is a file = %v, want one warning naming it", records)
	}
}
