package image

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"

	"phenix/store"
	"phenix/tmpl"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/mm/mmcli"
	"phenix/util/shell"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"
	"golang.org/x/exp/slices"
)

const (
	V_VERBOSE   int = 1
	V_VVERBOSE  int = 2
)

var (
	ErrMinicccNotFound   = fmt.Errorf("miniccc executable not found")
	ErrProtonukeNotFound = fmt.Errorf("protonuke executable not found")
)

// SetupImage sets a correct mirror based on the Release name if it wasn't
// set by the user and also sets some default packages. Based on the variant
// value, specific constants will be included during the create sub-command.
// The values are passed from the `constants.go` file. An error will be
// returned if the variant value is not valid (acceptable values are `minbase`
// or `mingui`).
func SetupImage(img *v1.Image) error {
	debian 	:= []string{"jessie", "stretch", "buster", "bullseye", "bookworm"}
	kali 	:= []string{"kali-dev", "kali-rolling", "kali-last-snapshot", "kali-bleeding-edge"}

	// If mirror is the default value, make sure it is correct based on the Release
	if img.Mirror == "http://us.archive.ubuntu.com/ubuntu" {
		if slices.Contains(debian, img.Release) {
			img.Mirror = "http://ftp.us.debian.org/debian"
		} else if slices.Contains(kali, img.Release) {
			img.Mirror = "http://http.kali.org/kali"
		}
	}

	// If not specified, set default package components
	if len(img.Components) == 0 {
		if slices.Contains(kali, img.Release) {
			img.Components = append(img.Components, KALI_COMPONENTS...)
		} else {
			img.Components = append(img.Components, DEBIAN_COMPONENTS...)
		}
	}

	img.Scripts = make(map[string]string)

	if !img.SkipDefaultPackages {
		img.Packages = append(img.Packages, DEFAULT_PACKAGES...)
	}

	switch img.Variant {
	case "minbase":
		if slices.Contains(kali, img.Release) {
			img.Packages = append(img.Packages, KALI_PACKAGES...)
		} else if slices.Contains(debian, img.Release) {
			img.Packages = append(img.Packages, DEBIAN_PACKAGES...)
		} else { // "xenial", "bionic", "focal", "jammy", "noble" ...
			img.Packages = append(img.Packages, UBUNTU_PACKAGES...)
		}
	case "mingui":
		if slices.Contains(kali, img.Release) {
			img.Packages = append(img.Packages, KALI_PACKAGES...)
			img.Packages = append(img.Packages, KALI_MINGUI_PACKAGES...)
			addScriptToImage(img, "POSTBUILD_KALI_GUI", POSTBUILD_KALI_GUI)
		} else if slices.Contains(debian, img.Release) {
			img.Packages = append(img.Packages, DEBIAN_PACKAGES...)
			img.Packages = append(img.Packages, DEBIAN_MINGUI_PACKAGES...)
			addScriptToImage(img, "POSTBUILD_GUI", POSTBUILD_GUI)
		} else { // "xenial", "bionic", "focal", "jammy", "noble" ...
			img.Packages = append(img.Packages, UBUNTU_PACKAGES...)
			img.Packages = append(img.Packages, UBUNTU_MINGUI_PACKAGES...)
			addScriptToImage(img, "POSTBUILD_GUI", POSTBUILD_GUI)
		}
	default:
		return fmt.Errorf("variant %s is not implemented", img.Variant)
	}

	addScriptToImage(img, "POSTBUILD_APT_CLEANUP", POSTBUILD_APT_CLEANUP)

	switch img.Variant {
	case "minbase", "mingui":
		addScriptToImage(img, "POSTBUILD_NO_ROOT_PASSWD", POSTBUILD_NO_ROOT_PASSWD)
		addScriptToImage(img, "POSTBUILD_PHENIX_HOSTNAME", POSTBUILD_PHENIX_HOSTNAME)
		addScriptToImage(img, "POSTBUILD_PHENIX_BASE", POSTBUILD_PHENIX_BASE)
	default:
		return fmt.Errorf("variant %s is not implemented", img.Variant)
	}

	if len(img.ScriptPaths) > 0 {
		for _, p := range img.ScriptPaths {
			if err := addScriptToImage(img, p, ""); err != nil {
				return fmt.Errorf("adding script %s to image config: %w", p, err)
			}
		}
	}

	return nil
}

// Create collects image values from user input at command line, creates an
// image configuration, and then persists it to the store. SetupImage is used
// to set default packages and constants. This sub-command requires an image
// `name`. It will return any errors encoutered while creating the
// configuration.
func Create(img *v1.Image) error {
	if img.Name == "" {
		return fmt.Errorf("image name is required to create an image")
	}

	if err := SetupImage(img); err != nil {
		return fmt.Errorf("setting up image: %w", err)
	}

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Image",
		Metadata: store.ConfigMetadata{Name: img.Name},
		Spec:     structs.MapDefaultCase(img, structs.CASESNAKE),
	}

	if err := store.Create(&c); err != nil {
		return fmt.Errorf("storing image config: %w", err)
	}

	return nil
}

// CreateFromConfig will take in an existing image configuration by name and
// modify overlay, packages, and scripts as passed by the user. It will then
// persist a new image configuration to the store. Any errors enountered will be
// passed when creating a new image configuration, retrieving the exisitng image
// configuration file, or storing the new image configuration file in the store.
func CreateFromConfig(name, saveas string, overlays, packages, scripts []string) error {
	c, err := store.NewConfig("image/" + name)
	if err != nil {
		return fmt.Errorf("creating new image config for %s: %w", name, err)
	}

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err := mapstructure.Decode(c.Spec, &img); err != nil {
		return fmt.Errorf("decoding image spec: %w", err)
	}

	c.Metadata.Name = saveas

	if len(overlays) > 0 {
		img.Overlays = append(img.Overlays, overlays...)
	}

	if len(packages) > 0 {
		img.Packages = append(img.Packages, packages...)
	}

	if len(scripts) > 0 {
		for _, s := range scripts {
			if err := addScriptToImage(&img, s, ""); err != nil {
				return fmt.Errorf("adding script %s to image config: %w", s, err)
			}
		}
	}

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err := store.Create(c); err != nil {
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
func Build(ctx context.Context, name string, verbosity int, cache bool, dryrun bool, output string) error {
	var img v1.Image
	var filename string

	if strings.Contains(name, ".vmdb") {
		filename = name
		name = strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	} else {
		c, _ := store.NewConfig("image/" + name)

		if err := store.Get(c); err != nil {
			return fmt.Errorf("getting image config %s from store: %w", name, err)
		}

		if err := mapstructure.Decode(c.Spec, &img); err != nil {
			return fmt.Errorf("decoding image spec: %w", err)
		}

		img.Cache = cache

		// The Kali package repos use `kali-rolling` as the release name.
		if img.Release == "kali" {
			img.Release = "kali-rolling"
		}

		filename = output + "/" + name + ".vmdb"

		if err := tmpl.CreateFileFromTemplate("vmdb.tmpl", img, filename); err != nil {
			return fmt.Errorf("generate vmdb config from template: %w", err)
		}
	}

	if !dryrun && !shell.CommandExists("vmdb2") {
		return fmt.Errorf("vmdb2 app does not exist in your path")
	}

	args := []string{
		filename,
		"--output", output + "/" + name,
		"--rootfs-tarball", output + "/" + name + ".tar",
	}

	if verbosity >= V_VERBOSE {
		args = append(args, "-v")
	}

	if verbosity >= V_VVERBOSE {
		args = append(args, "--log", output + "/" + name + ".log")
	}

	if dryrun {
		fmt.Printf("DRY RUN: vmdb2 %s\n", strings.Join(args, " "))
	} else {
		cmd := exec.Command("vmdb2", args...)

		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting vmdb2 command: %w", err)
		}

		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				fmt.Println(scanner.Text())
			}
		}()

		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				fmt.Println(scanner.Text())
			}
		}()

		if err := cmd.Wait(); err != nil {
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

		if err := mapstructure.Decode(c.Spec, spec); err != nil {
			return nil, fmt.Errorf("decoding image spec: %w", err)
		}

		img := types.Image{Metadata: c.Metadata, Spec: spec}

		images = append(images, img)
	}

	return images, nil
}

// Update retrieves the named image configuration file from the store and will
// update scripts. First, it will verify the script is present on disk. If so,
// it will remove the existing script from the configuration file and update the
// file with updated. It will return any errors encountered during the process
// of creating a new image configuration, decoding it, or updating it in the
// store.
func Update(name string) error {
	c, err := store.NewConfig("image/" + name)
	if err != nil {
		return fmt.Errorf("creating new image config for %s: %w", name, err)
	}

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err := mapstructure.Decode(c.Spec, &img); err != nil {
		return fmt.Errorf("decoding image spec: %w", err)
	}

	scripts := img.Scripts

	if len(scripts) > 0 {
		for k := range scripts {
			if _, err := os.Stat(k); err == nil {
				delete(img.Scripts, k)

				if err := addScriptToImage(&img, k, ""); err != nil {
					return fmt.Errorf("adding script %s to image config: %w", k, err)
				}
			}
		}
	}

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
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

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err := mapstructure.Decode(c.Spec, &img); err != nil {
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
			if err := addScriptToImage(&img, s, ""); err != nil {
				return fmt.Errorf("adding script %s to image config: %w", s, err)
			}
		}
	}

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
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

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting config from store: %w", err)
	}

	var img v1.Image

	if err := mapstructure.Decode(c.Spec, &img); err != nil {
		return fmt.Errorf("decoding image spec: %w", err)
	}

	if len(overlays) > 0 {
		o := img.Overlays[:0]

		for _, overlay := range img.Overlays {
			var match bool

			for _, n := range overlays {
				if n == overlay {
					match = true
					break
				}
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

			for _, n := range packages {
				if n == pkg {
					match = true
					break
				}
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
	}

	c.Spec = structs.MapDefaultCase(img, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		return fmt.Errorf("updating image config in store: %w", err)
	}

	return nil
}

func InjectMiniExe(exe, disk, svc string) error {
	// Assume partition 1 if no partition is specified.
	if parts := strings.Split(disk, ":"); len(parts) == 1 {
		disk = disk + ":1"
	}

	tmp := os.TempDir() + "/phenix"

	if err := os.MkdirAll(tmp, 0755); err != nil {
		return fmt.Errorf("creating temp phenix base directory: %w", err)
	}

	tmp, err := ioutil.TempDir(tmp, "")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}

	defer os.RemoveAll(tmp)

	var injects []string

	if path.Ext(exe) == ".exe" { // assume Windows
		// /opt/minimega/bin/miniccc.exe --> miniccc
		base := strings.TrimSuffix(path.Base(exe), path.Ext(exe))

		if base != "miniccc" && base != "protonuke" {
			return fmt.Errorf("only miniccc.exe and protonuke.exe are supported for Windows")
		}

		switch base {
		case "miniccc":
			if svc == "startup" {
				if err := tmpl.RestoreAsset(tmp, fmt.Sprintf("%s/%s-scheduler.cmd", base, base)); err != nil {
					return fmt.Errorf("restoring %s startup scheduler for Windows: %w", base, err)
				}

				injects = []string{
					tmp + fmt.Sprintf(`/%s/%s-scheduler.cmd:"/ProgramData/Microsoft/Windows/Start Menu/Programs/Startup/%s-scheduler.cmd"`, base, base, base),
					exe + fmt.Sprintf(":/minimega/%s.exe", base),
				}
			} else {
				injects = []string{
					exe + fmt.Sprintf(":/minimega/%s.exe", base),
				}
			}
		case "protonuke":
			// We're not creating a default Windows Startup file for protonuke to
			// start it as a service at boot since its command line arguments are
			// dynamic. Users or apps wishing to leverage protonuke on Windows hosts
			// need to inject their own Windows Startup file or use miniccc to start
			// protonuke.
			injects = []string{
				exe + fmt.Sprintf(":/minimega/%s.exe", base),
			}
		}
	} else {
		// /opt/minimega/bin/miniccc --> miniccc
		base := path.Base(exe)

		if base != "miniccc" && base != "protonuke" && base != "minirouter" {
			return fmt.Errorf("only miniccc, protonuke, and minirouter are supported for Linux")
		}

		if err := os.MkdirAll(tmp+fmt.Sprintf("/%s/symlinks", base), 0755); err != nil {
			return fmt.Errorf("creating symlinks directory path: %w", err)
		}

		switch svc {
		case "systemd":
			if err := tmpl.RestoreAsset(tmp, fmt.Sprintf("%s/%s.service", base, base)); err != nil {
				return fmt.Errorf("restoring %s systemd service for Linux: %w", base, err)
			}

			if err := os.Symlink(fmt.Sprintf("../%s.service", base), tmp+fmt.Sprintf("/%s/symlinks/%s.service", base, base)); err != nil {
				return fmt.Errorf("generating systemd service link for Linux: %w", err)
			}

			injects = []string{
				tmp + fmt.Sprintf("/%s/%s.service:/etc/systemd/system/%s.service", base, base, base),
				tmp + fmt.Sprintf("/%s/symlinks/%s.service:/etc/systemd/system/multi-user.target.wants/%s.service", base, base, base),
				exe + fmt.Sprintf(":/usr/local/bin/%s", base),
			}
		case "sysinitv":
			if err := tmpl.RestoreAsset(tmp, fmt.Sprintf("%s/%s.init", base, base)); err != nil {
				return fmt.Errorf("restoring %s sysinitv service for Linux: %w", base, err)
			}

			os.Chmod(tmp+fmt.Sprintf("/%s/%s.init", base, base), 0755)

			if err := os.Symlink(fmt.Sprintf("../init.d/%s", base), tmp+fmt.Sprintf("/%s/symlinks/S99-%s", base, base)); err != nil {
				return fmt.Errorf("generating sysinitv service link for Linux: %w", err)
			}

			injects = []string{
				tmp + fmt.Sprintf("/%s/%s.init:/etc/init.d/%s", base, base, base),
				tmp + fmt.Sprintf("/%s/symlinks/S99-%s:/etc/rc5.d/S99-%s", base, base, base),
				exe + fmt.Sprintf(":/usr/local/bin/%s", base),
			}
		default:
			return fmt.Errorf("unknown service %s specified", svc)
		}

		// Ensure miniccc is injected if minirouter was just injected, since
		// minirouter depends on miniccc. These injection activities are idempotent,
		// so injecting miniccc if it was already injected shouldn't hurt anything.
		if base == "minirouter" {
			if err := InjectMiniExe(path.Dir(exe)+"/miniccc", disk, svc); err != nil {
				return fmt.Errorf("error injecting minirouter dependency miniccc: %w", err)
			}
		}
	}

	if err := inject(disk, injects...); err != nil {
		return fmt.Errorf("injecting files into disk: %w", err)
	}

	return nil
}

func addScriptToImage(img *v1.Image, name, script string) error {
	if script == "" {
		u, err := url.Parse(name)
		if err != nil {
			return fmt.Errorf("parsing script path: %w", err)
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
			resp, err := http.Get(name)
			if err != nil {
				return fmt.Errorf("getting script via HTTP(s): %w", err)
			}

			body = resp.Body
		case "file":
			body, err = os.Open(loc)
			if err != nil {
				return fmt.Errorf("opening script file: %w", err)
			}
		default:
			return fmt.Errorf("scheme %s not supported for scripts", u.Scheme)
		}

		defer body.Close()

		contents, err := ioutil.ReadAll(body)
		if err != nil {
			return fmt.Errorf("processing script %s: %w", name, err)
		}

		script = string(contents)
	}

	img.Scripts[name] = script
	img.ScriptOrder = append(img.ScriptOrder, name)

	return nil
}

func inject(disk string, injects ...string) error {
	files := strings.Join(injects, " ")

	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf("disk inject %s files %s", disk, files)

	if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
		return fmt.Errorf("injecting files into disk %s: %w", disk, err)
	}

	return nil
}
