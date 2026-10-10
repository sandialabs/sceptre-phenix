package builder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"phenix/types/builder"
	"phenix/util/plog"
)

// ServerIconOwner is the owner that the icon library records for an icon the
// server adds from one of its template files: no name at all. A phenix user
// name may hold any printable character, so an account can take any name. But
// no name is empty, because every request names its user, and the service
// refuses an empty caller. No account owns such an icon, so only the holders
// of the builder-icons permissions rename or delete it.
const ServerIconOwner = ""

// MaxServerTemplateFiles is the most template files the server reads from its
// template directory. The server skips the files past that number, in name
// order, and records each one as a problem.
const MaxServerTemplateFiles = 50

// ServerIconReserve is how many of the [MaxIcons] places of the icon library
// the template files of the server leave to users. The server adds their icons
// only while the library holds fewer than MaxIcons - ServerIconReserve icons.
const ServerIconReserve = MaxIcons / 2

// The identifiers of the collections and templates the server reads from its
// template files: serverIDPrefix and the first serverIDBytes bytes, in hex,
// of a SHA-256 of what names the item.
const (
	serverIDPrefix = "server-"
	serverIDBytes  = 12
)

// templateFileExtensions are the extensions, in lower case, of the files the
// server reads from its template directory.
var templateFileExtensions = []string{".yaml", ".yml", ".json"} //nolint:gochecknoglobals // immutable list

// ServerCollection is a collection of templates that the server read from one
// of its template files at start. It is held in memory only, and is read only
// for every user. The directory keeps it, and the server reads it again at the
// next start.
type ServerCollection struct {
	// ID is derived from the name of the file, so it is the same at every
	// start.
	ID          string
	Name        string
	Description string
	// File is the name of the file in the template directory.
	File string
	// Templates are the templates of the file, in file order. Each has an ID
	// derived from the name of the file and its own name, ignoring case.
	Templates []builder.Template
}

// TemplateFileProblem is a file of the template directory the server did not
// read, and why.
type TemplateFileProblem struct {
	File   string
	Reason string
}

// TemplateDirectory is what [ReadTemplateDirectory] found in a template
// directory.
type TemplateDirectory struct {
	// Collections are the collections of the usable files, in name order of
	// the files.
	Collections []ServerCollection
	// Icons are the custom icons each of those files carries, by icon name,
	// in the order of Collections.
	Icons []map[string]builder.Icon
	// Problems are the files that could not be used.
	Problems []TemplateFileProblem
	// Missing is set when the directory does not exist.
	Missing bool
}

// ReadTemplateDirectory reads the template files directly in directory. These
// are the files whose names end in .yaml, .yml or .json, in any case, and do
// not start with "." (hidden files, and the copies of file attributes that
// some file systems make). It does not read subdirectories.
//
// It opens the directory through [os.Root], as for a Builder file (see
// [ReadDocumentFile]). Thus it follows a symbolic link only when the link
// stays in the directory. Each file must be a regular file of at most
// [builder.MaxTemplateFileBytes] whose text [builder.ParseTemplateFile]
// accepts. Any other file is a problem, with the reason, and the other files
// are read. It reads at most [MaxServerTemplateFiles] files. A directory that
// does not exist is not an error: it has no files. A directory that cannot be
// opened is an error.
func ReadTemplateDirectory(directory string) (*TemplateDirectory, error) {
	found := &TemplateDirectory{Collections: []ServerCollection{}, Icons: nil, Problems: nil, Missing: false}

	root, err := os.OpenRoot(directory)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		found.Missing = true

		return found, nil
	case err != nil:
		return nil, fmt.Errorf("opening the template directory %s: %w", directory, err)
	}

	defer func() { _ = root.Close() }()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("listing the template directory %s: %w", directory, err)
	}

	read := 0

	for _, entry := range entries {
		name := entry.Name()
		extension := strings.ToLower(filepath.Ext(name))

		if strings.HasPrefix(name, ".") || !slices.Contains(templateFileExtensions, extension) {
			continue
		}

		if read >= MaxServerTemplateFiles {
			found.Problems = append(found.Problems, TemplateFileProblem{
				File:   name,
				Reason: fmt.Sprintf("the directory holds more than %d template files", MaxServerTemplateFiles),
			})

			continue
		}

		read++

		file, reason := readTemplateFile(root, name)
		if reason != "" {
			found.Problems = append(found.Problems, TemplateFileProblem{File: name, Reason: reason})

			continue
		}

		found.Collections = append(found.Collections, serverCollection(name, file))
		found.Icons = append(found.Icons, file.Icons)
	}

	return found, nil
}

// readTemplateFile reads and parses the template file of the given name in
// root, or says why it cannot be used.
func readTemplateFile(root *os.Root, name string) (*builder.TemplateFile, string) {
	// Opening a named pipe for reading waits for a writer unless the open is
	// non-blocking.
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Sprintf("cannot be read: %v", err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()

	switch {
	case err != nil:
		return nil, fmt.Sprintf("cannot be read: %v", err)
	case !info.Mode().IsRegular():
		return nil, notRegular
	case info.Size() > builder.MaxTemplateFileBytes:
		return nil, fmt.Sprintf("is larger than %d bytes", builder.MaxTemplateFileBytes)
	}

	// The file can grow after its size was read.
	data, err := io.ReadAll(io.LimitReader(file, builder.MaxTemplateFileBytes+1))

	switch {
	case err != nil:
		return nil, fmt.Sprintf("cannot be read: %v", err)
	case len(data) > builder.MaxTemplateFileBytes:
		return nil, fmt.Sprintf("is larger than %d bytes", builder.MaxTemplateFileBytes)
	}

	parsed, err := builder.ParseTemplateFile(data)
	if err != nil {
		return nil, fmt.Sprintf("is not a valid template file: %s", templateFileReason(err))
	}

	return parsed, ""
}

// templateFileReason says why [builder.ParseTemplateFile] refused a file,
// without the prefix that every refusal starts with.
func templateFileReason(err error) string {
	return strings.TrimPrefix(err.Error(), builder.ErrInvalidTemplateFile.Error()+": ")
}

// serverCollection returns the collection a template file of the given name
// holds, with identifiers derived from the name of the file.
func serverCollection(fileName string, file *builder.TemplateFile) ServerCollection {
	templates := make([]builder.Template, 0, len(file.Templates))

	for i := range file.Templates {
		name := strings.ToLower(strings.TrimSpace(file.Templates[i].Name))

		templates = append(templates, file.Templates[i].Template(serverID("template", fileName, name)))
	}

	return ServerCollection{
		ID:          serverID("collection", fileName),
		Name:        file.Name,
		Description: file.Description,
		File:        fileName,
		Templates:   templates,
	}
}

// serverID returns the identifier of an item the server read from a
// template file, from what names it.
func serverID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))

	return serverIDPrefix + hex.EncodeToString(sum[:serverIDBytes])
}

// LoadServerTemplates reads the template files of directory (see
// [ReadTemplateDirectory]). It keeps their collections as the collections of
// the server, in place of any it held, and returns the files it skipped. An
// empty directory name reads nothing.
//
// It logs each skipped file with its name and the reason. It logs a directory
// that does not exist at debug level only, and a directory that cannot be read
// as a warning. Then the server has no collections of its own.
//
// It adds to the icon library the custom icons the files carry that the
// library does not have. It adds them as icons of [ServerIconOwner], apart
// from what any user may upload, while the library leaves [ServerIconReserve]
// places to users. It skips the icons past that, with one warning. A name the
// library holds with other bytes keeps the icon of the library, and
// LoadServerTemplates logs this. It also logs an icon the library refuses.
func (s *Service) LoadServerTemplates(ctx context.Context, directory string) []TemplateFileProblem {
	collections := []ServerCollection{}

	defer func() { s.serverTemplates.Store(&collections) }()

	if directory == "" {
		return nil
	}

	found, err := ReadTemplateDirectory(directory)

	switch {
	case err != nil:
		plog.Warn(plog.TypeSystem, "builder template directory cannot be read", "directory", directory, "err", err)

		return nil
	case found.Missing:
		plog.Debug(plog.TypeSystem, "builder template directory does not exist", "directory", directory)

		return nil
	}

	for _, problem := range found.Problems {
		plog.Warn(
			plog.TypeSystem, "skipping builder template file",
			"directory", directory, "file", problem.File, "reason", problem.Reason,
		)
	}

	s.addServerIcons(ctx, found)

	collections = found.Collections

	plog.Info(
		plog.TypeSystem, "read builder template files",
		"directory", directory, "collections", len(collections), "skipped", len(found.Problems),
	)

	return found.Problems
}

// errNoRoomForServerIcon is the error [Service.addServerIcon] refuses an icon
// with when the library leaves no more places to the template files of the
// server.
var errNoRoomForServerIcon = errors.New("the icon library leaves no more room to template files")

// addServerIcons adds the custom icons the template files carry to the icon
// library, as [Service.LoadServerTemplates] says. It adds them in file order
// and, in a file, in the order of the icon names. It lists the library once,
// for how many icons it holds. Then it finds the name of each icon separately.
func (s *Service) addServerIcons(ctx context.Context, found *TemplateDirectory) {
	library, err := s.ListIcons(ctx)
	if err != nil {
		plog.Warn(
			plog.TypeSystem, "builder template file icons cannot be added: the icon library cannot be listed",
			"err", err,
		)

		return
	}

	held, limit := len(library), MaxIcons-ServerIconReserve
	skipped := 0

	for i := range found.Collections {
		file, icons := found.Collections[i].File, found.Icons[i]

		for _, name := range slices.Sorted(maps.Keys(icons)) {
			// The file was validated, so the data is a PNG the library accepts.
			data, err := base64.StdEncoding.Strict().DecodeString(icons[name].Data)
			if err != nil {
				continue
			}

			icon, added, err := s.addServerIcon(name, data, held < limit)

			var taken *IconNameTakenError

			switch {
			case errors.Is(err, errNoRoomForServerIcon):
				skipped++
			case errors.As(err, &taken):
				plog.Warn(
					plog.TypeSystem, "builder template file icon differs from the icon library's, which is kept",
					"file", file, "icon", name, "library", taken.Icon, "owner", iconOwnerText(taken.Owner),
				)
			case err != nil:
				plog.Warn(
					plog.TypeSystem, "builder template file icon cannot be added to the icon library",
					"file", file, "icon", name, "err", err,
				)
			case added:
				held++

				plog.Info(
					plog.TypeSystem, "added builder template file icon to the icon library",
					"file", file, "icon", icon.Name,
				)
			}
		}
	}

	if skipped > 0 {
		plog.Warn(
			plog.TypeSystem, "builder icon library has no room for more template file icons",
			"skipped", skipped, "icons", held, "limit", limit, "reserved", ServerIconReserve,
		)
	}
}

// addServerIcon adds an icon of a template file to the icon library as an icon
// of [ServerIconOwner], under its name. It does this as [Service.AddIcon] adds
// the icon of a user, but apart from what any user may upload. A name that
// already names the same image is that icon (false). A name that names another
// image is an [IconNameTakenError], whatever room is left. Without room, it
// refuses a new icon with errNoRoomForServerIcon.
func (s *Service) addServerIcon(name string, upload []byte, room bool) (*LibraryIcon, bool, error) {
	data, width, height, err := iconUpload(name, upload)
	if err != nil {
		return nil, false, err
	}

	if existing, found, err := s.iconOfName(name, builder.IconID(data)); found || err != nil {
		return existing, false, err
	}

	if !room {
		return nil, false, errNoRoomForServerIcon
	}

	return s.createIcon(ServerIconOwner, name, data, width, height)
}

// ServerCollections returns the collections that the server read from its
// template files (see [Service.LoadServerTemplates]), in name order of the
// files. The lists are copies. The template specs are shared and must not be
// changed.
func (s *Service) ServerCollections() []ServerCollection {
	held := s.serverTemplates.Load()
	if held == nil {
		return []ServerCollection{}
	}

	collections := slices.Clone(*held)

	for i := range collections {
		collections[i].Templates = slices.Clone(collections[i].Templates)
	}

	return collections
}
