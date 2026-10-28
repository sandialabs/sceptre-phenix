package image

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"

	"phenix/store"
	"phenix/tmpl"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/shell"
)

const (
	VVerbose  int = 1
	VVVerbose int = 2
)

const miniccc = "miniccc"
const protonuke = "protonuke"

// SetupImage sets a correct mirror based on the Release name if it wasn't
// set by the user and also sets some default packages. Based on the variant
// value, specific constants will be included during the create sub-command.
// The values are passed from the `constants.go` file. An error will be
// returned if the variant value is not valid (acceptable values are `minbase`
// or `mingui`).
func SetupImage(img *v1.Image) error { //nolint:funlen // complex logic
	debian := []string{"jessie", "stretch", "buster", "bullseye", "bookworm", "trixie", "forky"}
	kali := []string{"kali", "kali-dev", "kali-rolling", "kali-last-snapshot", "kali-bleeding-edge"}

	// If mirror is the default value, make sure it is correct based on the Release
	if img.Mirror == "http://us.archive.ubuntu.com/ubuntu" {
		switch {
		case slices.Contains(debian, img.Release):
			img.Mirror = "http://ftp.us.debian.org/debian"
		case slices.Contains(kali, img.Release):
			img.Mirror = "http://http.kali.org/kali"
		}
	}

	// If not specified, set default package components
	if len(img.Components) == 0 {
		switch {
		case slices.Contains(kali, img.Release):
			img.Components = append(img.Components, KaliComponents...)
		case slices.Contains(debian, img.Release):
			img.Components = append(img.Components, DebianComponents...)
		default:
			img.Components = append(img.Components, UbuntuComponents...)
		}
	}

	img.Scripts = make(map[string]string)

	if !img.SkipDefaultPackages {
		img.Packages = append(img.Packages, DefaultPackages...)
	}

	switch img.Variant {
	case "minbase":
		switch {
		case slices.Contains(kali, img.Release):
			img.Packages = append(img.Packages, KaliPackages...)
		case slices.Contains(debian, img.Release):
			img.Packages = append(img.Packages, DebianPackages...)
		default: // "focal", "jammy", "noble", "resolute" ...
			img.Packages = append(img.Packages, UbuntuPackages...)
		}
	case "mingui":
		switch {
		case slices.Contains(kali, img.Release):
			img.Packages = append(img.Packages, KaliPackages...)
			img.Packages = append(img.Packages, KaliMinGUIPackages...)
			_ = addScriptToImage(img, "POSTBUILD_KALI_GUI", PostbuildKaliGUI)
		case slices.Contains(debian, img.Release):
			img.Packages = append(img.Packages, DebianPackages...)
			img.Packages = append(img.Packages, DebianMinGUIPackages...)
			_ = addScriptToImage(img, "POSTBUILD_GUI", PostbuildGUI)
		default: // "focal", "jammy", "noble", "resolute" ...
			img.Packages = append(img.Packages, UbuntuPackages...)
			img.Packages = append(img.Packages, UbuntuMinGUIPackages...)
			_ = addScriptToImage(img, "POSTBUILD_GUI", PostbuildGUI)
		}
	default:
		return fmt.Errorf("variant %s is not implemented", img.Variant)
	}

	_ = addScriptToImage(img, "POSTBUILD_APT_CLEANUP", PostbuildAptCleanup)

	switch img.Variant {
	case "minbase", "mingui":
		_ = addScriptToImage(img, "POSTBUILD_NO_ROOT_PASSWD", PostbuildNoRootPasswd)
		_ = addScriptToImage(img, "POSTBUILD_PHENIX_HOSTNAME", PostbuildPhenixHostname)
		_ = addScriptToImage(img, "POSTBUILD_PHENIX_BASE", PostbuildPhenixBase)
	default:
		return fmt.Errorf("variant %s is not implemented", img.Variant)
	}

	if len(img.ScriptPaths) > 0 {
		for _, p := range img.ScriptPaths {
			err := addScriptToImage(img, p, "")
			if err != nil {
				return fmt.Errorf("adding script %s to image config: %w", p, err)
			}
		}
	}

	return nil
}

// Create collects image values from user input at command line, creates an
// image configuration, and then persists it to the store. SetupImage is used
// to set default packages and constants. This sub-command requires an image
// `name`. It will return any errors encountered while creating the
// configuration.
func Create(img *v1.Image) error {
	if img.Name == "" {
		return errors.New("image name is required to create an image")
	}

	if err := ValidateSize(img.Size); err != nil {
		return fmt.Errorf("invalid image size %q: %w", img.Size, err)
	}

	err := SetupImage(img)
	if err != nil {
		return fmt.Errorf("setting up image: %w", err)
	}

	c := store.Config{ //nolint:exhaustruct // partial initialization
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Image",
		Metadata: store.ConfigMetadata{Name: img.Name}, //nolint:exhaustruct // partial initialization
		Spec:     structs.MapDefaultCase(img, structs.CASESNAKE),
	}

	err = store.Create(&c)
	if err != nil {
		return fmt.Errorf("storing image config: %w", err)
	}

	return nil
}

// CreateFromConfig will take in an existing image configuration by name and
// modify overlay, packages, and scripts as passed by the user. It will then
// persist a new image configuration to the store. Any errors encountered will be
// passed when creating a new image configuration, retrieving the existing image
// configuration file, or storing the new image configuration file in the store.
func CreateFromConfig(name, saveas string, overlays, packages, scripts []string) error {
	c, err := store.NewConfig("image/" + name)
	if err != nil {
		return fmt.Errorf("creating new image config for %s: %w", name, err)
	}

	if err = store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err = mapstructure.Decode(c.Spec, &img); err != nil {
		return fmt.Errorf("decoding image spec: %w", err)
	}

	// the new config is created now, under its own name
	c.Metadata.Name, c.Metadata.Created = saveas, ""
	img.Name = saveas

	if len(overlays) > 0 {
		img.Overlays = append(img.Overlays, overlays...)
	}

	if len(packages) > 0 {
		img.Packages = append(img.Packages, packages...)
	}

	if len(scripts) > 0 {
		for _, s := range scripts {
			err = addScriptToImage(&img, s, "")
			if err != nil {
				return fmt.Errorf("adding script %s to image config: %w", s, err)
			}
		}
	}

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err = store.Create(c); err != nil {
		return fmt.Errorf("storing new image config %s in store: %w", saveas, err)
	}

	return nil
}

// Build uses the image configuration `name` passed by users to build an image.
// If verbosity is set, `vmdb` will output progress as it builds the image.
// Otherwise, there will only be output if an error is encountered. The image
// configuration is used with a template to build the `vmdb` configuration file
// and then pass it to the shelled out `vmdb` command. This expects the `vmdb`
// application is in the `$PATH`. Any errors encountered will be returned during
// the process of getting an existing image configuration, decoding it,
// generating the `vmdb` verbosconfiguration file, or executing the `vmdb` command.
func Build(
	ctx context.Context,
	name string,
	verbosity int,
	cache bool,
	dryrun bool,
	output string,
) error {
	var (
		img      v1.Image
		filename string
	)

	if strings.Contains(name, ".vmdb") {
		filename = name
		name = strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	} else {
		c, _ := store.NewConfig("image/" + name)

		err := store.Get(c)
		if err != nil {
			return fmt.Errorf("getting image config %s from store: %w", name, err)
		}

		err = mapstructure.Decode(c.Spec, &img)
		if err != nil {
			return fmt.Errorf("decoding image spec: %w", err)
		}

		img.Cache = cache

		// The Kali package repos use `kali-rolling` as the release name.
		if img.Release == "kali" {
			img.Release = "kali-rolling"
		}

		filename = output + "/" + name + ".vmdb"

		err = tmpl.CreateFileFromTemplate("vmdb.tmpl", img, filename)
		if err != nil {
			return fmt.Errorf("generate vmdb config from template: %w", err)
		}
	}

	if !dryrun && !shell.CommandExists("vmdb2") {
		return errors.New("vmdb2 app does not exist in your path")
	}

	args := []string{
		filename,
		"--output", output + "/" + name,
		"--rootfs-tarball", output + "/" + name + ".tar",
	}

	if verbosity >= VVerbose {
		args = append(args, "-v")
	}

	if verbosity >= VVVerbose {
		args = append(args, "--log", output+"/"+name+".log")
	}

	if dryrun {
		fmt.Printf("DRY RUN: vmdb2 %s\n", strings.Join(args, " ")) //nolint:forbidigo // CLI output
	} else {
		cmd := exec.CommandContext(ctx, "vmdb2", args...)

		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()

		err := cmd.Start()
		if err != nil {
			return fmt.Errorf("starting vmdb2 command: %w", err)
		}

		// Wait closes the pipes, so all output must be read before it.
		var wg sync.WaitGroup

		for _, r := range []io.Reader{stdout, stderr} {
			wg.Add(1)

			go func() {
				defer wg.Done()

				scanner := bufio.NewScanner(r)
				for scanner.Scan() {
					fmt.Println(scanner.Text()) //nolint:forbidigo // CLI output
				}
			}()
		}

		wg.Wait()

		err = cmd.Wait()
		if err != nil {
			return fmt.Errorf("building image with vmdb2: %w", err)
		}
	}

	return nil
}

// List collects image configurations from the store. It returns a slice of all
// configurations. It will return any errors encountered while getting the list
// of image configurations.
func List() ([]types.Image, error) {
	configs, err := store.List("Image")
	if err != nil {
		return nil, fmt.Errorf("getting list of image configs from store: %w", err)
	}

	var images []types.Image

	for _, c := range configs {
		spec := new(v1.Image)

		err = mapstructure.Decode(c.Spec, spec)
		if err != nil {
			return nil, fmt.Errorf("decoding image spec: %w", err)
		}

		img := types.Image{Metadata: c.Metadata, Spec: spec}

		images = append(images, img)
	}

	return images, nil
}

// Update retrieves the named image configuration file from the store and
// rereads each of its scripts that is present on disk, keeping the order the
// scripts run in. It will return any errors encountered during the process of
// creating a new image configuration, decoding it, or updating it in the
// store.
func Update(name string) error {
	c, err := store.NewConfig("image/" + name)
	if err != nil {
		return fmt.Errorf("creating new image config for %s: %w", name, err)
	}

	if err = store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err = mapstructure.Decode(c.Spec, &img); err != nil {
		return fmt.Errorf("decoding image spec: %w", err)
	}

	reread := make(map[string]bool)

	// refresh replaces a configured script found on disk with its contents, once.
	refresh := func(k string) (bool, error) {
		if _, ok := img.Scripts[k]; !ok || reread[k] {
			return false, nil
		}

		// a script that is not a file on disk, such as a URL, keeps its contents
		exists, err := fileExists(k)
		if err != nil || !exists {
			return false, err
		}

		script, err := readScript(k)
		if err != nil {
			return false, fmt.Errorf("rereading script %s: %w", k, err)
		}

		img.Scripts[k] = script
		reread[k] = true

		return true, nil
	}

	// A reread script keeps its first place in the order, or goes last if it had none.
	// A name in the order that has no script is dropped.
	order := make([]string, 0, len(img.ScriptOrder))

	for _, k := range img.ScriptOrder {
		if _, ok := img.Scripts[k]; !ok || reread[k] {
			continue
		}

		if _, err = refresh(k); err != nil {
			return err
		}

		order = append(order, k)
	}

	for _, k := range slices.Sorted(maps.Keys(img.Scripts)) {
		if slices.Contains(order, k) {
			continue
		}

		ok, err := refresh(k)
		if err != nil {
			return err
		}

		if ok {
			order = append(order, k)
		}
	}

	img.ScriptOrder = order

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err = store.Update(c); err != nil {
		return fmt.Errorf("updating image config in store: %w", err)
	}

	return nil
}

// Append retrieves the named image configuration file from the store and will
// update it with overlays, packages, and scripts as passed by the user. It will
// return any errors encountered during the process of creating a new image
// configuration, decoding it, or updating it in the store.
func Append(name string, overlays, packages, scripts []string) error {
	c, err := store.NewConfig("image/" + name)
	if err != nil {
		return fmt.Errorf("creating new image config for %s: %w", name, err)
	}

	if err = store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err = mapstructure.Decode(c.Spec, &img); err != nil {
		return fmt.Errorf("decoding image spec: %w", err)
	}

	if len(overlays) > 0 {
		img.Overlays = append(img.Overlays, overlays...)
	}

	if len(packages) > 0 {
		img.Packages = append(img.Packages, packages...)
	}

	if len(scripts) > 0 {
		for _, s := range scripts {
			err = addScriptToImage(&img, s, "")
			if err != nil {
				return fmt.Errorf("adding script %s to image config: %w", s, err)
			}
		}
	}

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err = store.Update(c); err != nil {
		return fmt.Errorf("updating image config in store: %w", err)
	}

	return nil
}

// Remove will update an existing image configuration by removing the overlays,
// packages, and scripts as passed by the user. It will return any errors
// encountered during the process of creating a new image configuration,
// decoding it, or updating it in the store.
func Remove(name string, overlays, packages, scripts []string) error {
	c, err := store.NewConfig("image/" + name)
	if err != nil {
		return fmt.Errorf("creating new image config for %s: %w", name, err)
	}

	if err = store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err = mapstructure.Decode(c.Spec, &img); err != nil {
		return fmt.Errorf("decoding image spec: %w", err)
	}

	if len(overlays) > 0 {
		o := img.Overlays[:0]

		for _, overlay := range img.Overlays {
			var match bool

			if slices.Contains(overlays, overlay) {
				match = true
			}

			if !match {
				o = append(o, overlay)
			}
		}

		img.Overlays = o
	}

	if len(packages) > 0 {
		p := img.Packages[:0]

		for _, pkg := range img.Packages {
			var match bool

			if slices.Contains(packages, pkg) {
				match = true
			}

			if !match {
				p = append(p, pkg)
			}
		}

		img.Packages = p
	}

	if len(scripts) > 0 {
		for _, s := range scripts {
			delete(img.Scripts, s)
		}

		img.ScriptOrder = slices.DeleteFunc(img.ScriptOrder, func(s string) bool { return slices.Contains(scripts, s) })
	}

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err = store.Update(c); err != nil {
		return fmt.Errorf("updating image config in store: %w", err)
	}

	return nil
}

// InjectMiniExe injects exe, the miniccc, protonuke or minirouter
// executable, and its boot files for svc into disk with a minimega disk
// inject. A disk with no ":<partition>" suffix gets partition 1. It returns an
// error wrapping [mm.ErrUnsafeArg] when minimega cannot take exe, disk or the
// temporary directory as written.
func InjectMiniExe(exe, disk, svc string) error { //nolint:funlen // complex logic
	if err := validateInjectPaths(exe, disk); err != nil {
		return err
	}

	// Assume partition 1 if no partition is specified.
	if parts := strings.Split(disk, ":"); len(parts) == 1 {
		disk += ":1"
	}

	tmp := os.TempDir() + "/phenix"

	if err := validateTempDir(tmp); err != nil {
		return err
	}

	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return fmt.Errorf("creating temp phenix base directory: %w", err)
	}

	tmp, err := os.MkdirTemp(tmp, "")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	var injects []injectPair

	if ext := path.Ext(exe); strings.EqualFold(ext, ".exe") { // assume Windows
		// /opt/minimega/bin/miniccc.exe --> miniccc; Windows file names ignore case
		base := strings.ToLower(strings.TrimSuffix(path.Base(exe), ext))

		if base != miniccc && base != protonuke {
			return errors.New("only miniccc.exe and protonuke.exe are supported for Windows")
		}

		exePair := injectPair{src: exe, dst: fmt.Sprintf("/minimega/%s.exe", base)}

		switch {
		case base == miniccc && svc == "startup":
			err = tmpl.RestoreAsset(tmp, fmt.Sprintf("%s/%s-scheduler.cmd", base, base))
			if err != nil {
				return fmt.Errorf("restoring %s startup scheduler for Windows: %w", base, err)
			}

			injects = []injectPair{
				{
					src: fmt.Sprintf("%s/%s/%s-scheduler.cmd", tmp, base, base),
					dst: fmt.Sprintf("/ProgramData/Microsoft/Windows/Start Menu/Programs/Startup/%s-scheduler.cmd", base),
				},
				exePair,
			}
		default:
			// We're not creating a default Windows Startup file for protonuke to
			// start it as a service at boot since its command line arguments are
			// dynamic. Users or apps wishing to leverage protonuke on Windows hosts
			// need to inject their own Windows Startup file or use miniccc to start
			// protonuke.
			injects = []injectPair{exePair}
		}
	} else {
		// /opt/minimega/bin/miniccc --> miniccc
		base := path.Base(exe)

		if base != miniccc && base != protonuke && base != "minirouter" {
			return errors.New("only miniccc, protonuke, and minirouter are supported for Linux")
		}

		err = os.MkdirAll(tmp+fmt.Sprintf("/%s/symlinks", base), 0o750)
		if err != nil {
			return fmt.Errorf("creating symlinks directory path: %w", err)
		}

		switch svc {
		case "systemd":
			err = tmpl.RestoreAsset(tmp, fmt.Sprintf("%s/%s.service", base, base))
			if err != nil {
				return fmt.Errorf("restoring %s systemd service for Linux: %w", base, err)
			}

			err = os.Symlink(
				fmt.Sprintf("../%s.service", base),
				tmp+fmt.Sprintf("/%s/symlinks/%s.service", base, base),
			)
			if err != nil {
				return fmt.Errorf("generating systemd service link for Linux: %w", err)
			}

			injects = []injectPair{
				{
					src: fmt.Sprintf("%s/%s/%s.service", tmp, base, base),
					dst: fmt.Sprintf("/etc/systemd/system/%s.service", base),
				},
				{
					src: fmt.Sprintf("%s/%s/symlinks/%s.service", tmp, base, base),
					dst: fmt.Sprintf("/etc/systemd/system/multi-user.target.wants/%s.service", base),
				},
				{src: exe, dst: "/usr/local/bin/" + base},
			}
		case "sysinitv":
			err = tmpl.RestoreAsset(tmp, fmt.Sprintf("%s/%s.init", base, base))
			if err != nil {
				return fmt.Errorf("restoring %s sysinitv service for Linux: %w", base, err)
			}

			_ = os.Chmod(tmp+fmt.Sprintf("/%s/%s.init", base, base), 0o750) //nolint:gosec // G302 (mode > 0600): script must be executable

			err = os.Symlink(
				"../init.d/"+base,
				tmp+fmt.Sprintf("/%s/symlinks/S99-%s", base, base),
			)
			if err != nil {
				return fmt.Errorf("generating sysinitv service link for Linux: %w", err)
			}

			injects = []injectPair{
				{src: fmt.Sprintf("%s/%s/%s.init", tmp, base, base), dst: "/etc/init.d/" + base},
				{src: fmt.Sprintf("%s/%s/symlinks/S99-%s", tmp, base, base), dst: "/etc/rc5.d/S99-" + base},
				{src: exe, dst: "/usr/local/bin/" + base},
			}
		default:
			return fmt.Errorf("unknown service %s specified", svc)
		}

		// Ensure miniccc is injected if minirouter was just injected, since
		// minirouter depends on miniccc. These injection activities are idempotent,
		// so injecting miniccc if it was already injected shouldn't hurt anything.
		if base == "minirouter" {
			err = InjectMiniExe(path.Dir(exe)+"/"+miniccc, disk, svc)
			if err != nil {
				return fmt.Errorf("error injecting minirouter dependency miniccc: %w", err)
			}
		}
	}

	if err = inject(disk, injects...); err != nil {
		return fmt.Errorf("injecting files into disk: %w", err)
	}

	return nil
}

// validateInjectPaths returns an error wrapping [mm.ErrUnsafeArg] unless
// minimega takes exe and disk, double-quoted, as written. The exe path also
// must not have a ":", which minimega reads as the end of a source path.
func validateInjectPaths(exe, disk string) error {
	if err := mm.ValidateQuotedArg(exe); err != nil {
		return fmt.Errorf("executable path: %w", err)
	}

	if strings.Contains(exe, ":") {
		return fmt.Errorf("%w: executable path %q has a \":\"", mm.ErrUnsafeArg, exe)
	}

	if err := mm.ValidateQuotedArg(disk); err != nil {
		return fmt.Errorf("disk path: %w", err)
	}

	return nil
}

// validateTempDir returns an error wrapping [mm.ErrUnsafeArg] unless
// minimega takes dir, the base of the boot file source paths, double-quoted
// as written. The dir also must not have a ":".
func validateTempDir(dir string) error {
	if err := mm.ValidateQuotedArg(dir); err != nil {
		return fmt.Errorf("temporary directory: %w", err)
	}

	if strings.Contains(dir, ":") {
		return fmt.Errorf("%w: temporary directory %q has a \":\"", mm.ErrUnsafeArg, dir)
	}

	return nil
}

func addScriptToImage(img *v1.Image, name, script string) error {
	if script == "" {
		var err error

		if script, err = readScript(name); err != nil {
			return err
		}
	}

	if img.Scripts == nil {
		img.Scripts = make(map[string]string)
	}

	img.Scripts[name] = script

	// a script added again runs once, in its first place
	if !slices.Contains(img.ScriptOrder, name) {
		img.ScriptOrder = append(img.ScriptOrder, name)
	}

	return nil
}

// fileExists reports whether [os.Stat] finds path. An HTTP(S) URL is not a
// file. It returns the error from [os.Stat] when that error does not mean
// that path does not exist.
func fileExists(path string) (bool, error) {
	if u, err := url.Parse(path); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return false, nil
	}

	_, err := os.Stat(path)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		// a path under a regular file is not a file either
		return false, nil
	default:
		return false, fmt.Errorf("checking script %s: %w", path, err)
	}
}

// readScript reads the script at name, a file path or an HTTP(S) URL.
func readScript(name string) (string, error) {
	u, err := url.Parse(name)
	if err != nil {
		return "", fmt.Errorf("parsing script path: %w", err)
	}

	// Default to file scheme if no scheme provided.
	if u.Scheme == "" {
		u.Scheme = "file"
	}

	var (
		loc  = u.Host + u.Path
		body io.ReadCloser
	)

	switch u.Scheme {
	case "http", "https":
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, name, nil)
		if err != nil {
			return "", fmt.Errorf("creating request for script: %w", err)
		}

		var resp *http.Response
		resp, err = http.DefaultClient.Do(req) //nolint:gosec // G704 (SSRF): the user names the script URL
		if err != nil {
			return "", fmt.Errorf("getting script via HTTP(s): %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()

			return "", fmt.Errorf("getting script via HTTP(s): %s", resp.Status)
		}

		body = resp.Body
	case "file":
		body, err = os.Open(loc) //nolint:gosec // G703 (path traversal): the user names the script file
		if err != nil {
			return "", fmt.Errorf("opening script file: %w", err)
		}
	default:
		return "", fmt.Errorf("scheme %s not supported for scripts", u.Scheme)
	}

	defer func() { _ = body.Close() }()

	contents, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("processing script %s: %w", name, err)
	}

	return string(contents), nil
}

// injectPair is a file to inject and the path in the disk to inject it to.
type injectPair struct {
	src, dst string
}

func inject(disk string, injects ...injectPair) error {
	files := make([]string, 0, len(injects))
	for _, i := range injects {
		files = append(files, fmt.Sprintf(`"%s":"%s"`, i.src, i.dst))
	}

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf(`disk inject "%s" files %s`, disk, strings.Join(files, " "))

	err := mmcli.ErrorResponse(mmcli.Run(cmd))
	if err != nil {
		return fmt.Errorf("injecting files into disk %s: %w", disk, err)
	}

	return nil
}
