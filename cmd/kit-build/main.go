// kit-build is authoring tooling; it is deliberately separate from driver delivery.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/inframill/kit/internal/catalog"
	"github.com/inframill/kit/internal/packaging"
	"github.com/opencontainers/go-digest"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func command(dir, program string, args ...string) error {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func run() error {
	if len(os.Args) != 2 {
		return errors.New("one build action required")
	}
	root, e := os.Getwd()
	if e != nil {
		return e
	}
	ctx := context.Background()
	action := os.Args[1]
	name := os.Getenv("BLUEPRINT")
	version := env("VERSION", "0.1.0")
	registry := env("REGISTRY", "ghcr.io")
	ns := env("NAMESPACE", "inframill")
	plain := os.Getenv("PLAIN_HTTP") == "true"
	if !catalog.ValidVersion(version) || !catalog.ValidName(ns) {
		return errors.New("invalid exact version or namespace")
	}
	switch action {
	case "build":
		kv := env("KIT_VERSION", "0.1.0")
		var release struct {
			Version          string `yaml:"version"`
			InframillVersion string `yaml:"inframill_version"`
			Drivers          []struct {
				Name    string `yaml:"name"`
				Version string `yaml:"version"`
			} `yaml:"drivers"`
		}
		if !catalog.ValidVersion(kv) {
			return errors.New("invalid Kit version")
		}
		if e = packaging.ReadYAML(filepath.Join("releases/kits", kv+".yaml"), &release); e != nil {
			return e
		}
		if release.InframillVersion != catalog.InframillVersion || release.Version != kv || len(release.Drivers) != len(catalog.List()) {
			return errors.New("Kit descriptor differs from compiled catalog")
		}
		for _, d := range release.Drivers {
			if _, e = catalog.Get(d.Name, d.Version); e != nil {
				return e
			}
		}
		if e = os.MkdirAll("bin", 0755); e != nil {
			return e
		}
		return command(root, "go", "build", "-trimpath", "-ldflags=-X main.version="+kv, "-o", "bin/inframill-kit", "./cmd/inframill-kit")
	case "inframill:copy":
		lockBytes, err := os.ReadFile(filepath.Join(root, "releases/bundles", version+".lock.yaml"))
		if err != nil {
			return err
		}
		var lock struct {
			Inframill struct {
				Version  string `json:"version"`
				Binaries map[string]struct {
					Path   string `json:"path"`
					SHA256 string `json:"sha256"`
				} `json:"native_binaries"`
			} `json:"inframill"`
		}
		if err = json.Unmarshal(lockBytes, &lock); err != nil {
			return err
		}
		iv := lock.Inframill.Version
		if !catalog.ValidVersion(iv) {
			return errors.New("invalid Inframill release version")
		}
		entry, ok := lock.Inframill.Binaries[runtime.GOOS+"/"+runtime.GOARCH]
		if !ok || !filepath.IsLocal(entry.Path) || iv != catalog.InframillVersion {
			return errors.New("unsupported native release")
		}
		source := filepath.Join(env("INFRAMILL_DIST_DIR", filepath.Join(root, "..", "inframill", "dist")), entry.Path)
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return errors.New("native Inframill checksum mismatch")
		}
		if runtime.GOOS == "linux" {
			binary, e := elf.NewFile(bytes.NewReader(data))
			if e != nil {
				return e
			}
			want := elf.EM_X86_64
			if runtime.GOARCH == "arm64" {
				want = elf.EM_AARCH64
			}
			if binary.Machine != want {
				return errors.New("native architecture mismatch")
			}
		} else if runtime.GOOS == "darwin" {
			binary, e := macho.NewFile(bytes.NewReader(data))
			if e != nil {
				return e
			}
			want := macho.CpuAmd64
			if runtime.GOARCH == "arm64" {
				want = macho.CpuArm64
			}
			if binary.Cpu != want {
				return errors.New("native architecture mismatch")
			}
		}
		output, err := exec.Command(source, "--version").Output()
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(output)) != iv {
			return errors.New("native version mismatch")
		}
		if err = os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
			return err
		}
		destination := filepath.Join(root, "bin", "inframill")
		if err = os.WriteFile(destination, data, 0755); err != nil {
			return err
		}
		return os.Chmod(destination, 0755)
	case "config":
		selected := catalog.List()
		if input := os.Getenv("CONFIG_DRIVERS"); input != "" {
			var requirements []catalog.Release
			if e = json.Unmarshal([]byte(input), &requirements); e != nil {
				return e
			}
			selected = nil
			for _, requirement := range requirements {
				r, err := catalog.Get(requirement.Name, requirement.Version)
				if err != nil {
					return err
				}
				selected = append(selected, r)
			}
		}
		tools := map[string]string{}
		for _, r := range selected {
			if tool := os.Getenv(strings.ToUpper(r.Name) + "_PATH"); tool != "" {
				tools[r.Name], e = filepath.Abs(tool)
				if e != nil {
					return e
				}
			}
		}
		return json.NewEncoder(os.Stdout).Encode(catalog.Configuration(env("KIT_EXECUTABLE", filepath.Join(root, "bin/inframill-kit")), env("DATA_DIR", filepath.Join(root, ".inframill")), selected, tools))
	case "blueprint:requirements":
		release, _, err := packaging.Blueprint(blueprintRoot(root), name, version)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(release)
	case "blueprint:source-digest":
		if !catalog.ValidName(name) {
			return errors.New("invalid blueprint name")
		}
		b, e := packaging.Archive(filepath.Join(blueprintRoot(root), "blueprints", name, "versions", version))
		if e != nil {
			return e
		}
		fmt.Println(digest.FromBytes(b))
		return nil
	case "blueprint:package":
		d, e := packaging.Package(ctx, blueprintRoot(root), name, version)
		if e != nil {
			return e
		}
		fmt.Println(d.Digest)
		return nil
	case "blueprint:push":
		if !catalog.ValidName(name) {
			return errors.New("invalid blueprint name")
		}
		repo := registry + "/" + ns + "/blueprints/" + name
		d, e := packaging.Push(ctx, packaging.BlueprintLayout(blueprintRoot(root), name, version), version, repo, plain)
		if e != nil {
			return e
		}
		fmt.Println(repo + "@" + d.Digest.String())
		return nil
	case "blueprints:package", "blueprints:push":
		set := env("RELEASE_SET", "default")
		if !catalog.ValidName(set) {
			return errors.New("invalid release set")
		}
		var selections struct {
			Blueprints []struct {
				Name    string `yaml:"name"`
				Version string `yaml:"version"`
			} `yaml:"blueprints"`
		}
		if e = packaging.ReadYAML(filepath.Join(root, "releases/sets", set+".yaml"), &selections); e != nil {
			return e
		}
		var failures []error
		for _, s := range selections.Blueprints {
			if !catalog.ValidName(s.Name) || !catalog.ValidVersion(s.Version) {
				return errors.New("invalid release selection")
			}
			if action == "blueprints:package" {
				d, e := packaging.Package(ctx, blueprintRoot(root), s.Name, s.Version)
				if e != nil {
					failures = append(failures, e)
					fmt.Fprintln(os.Stderr, s.Name, s.Version, e)
				} else {
					fmt.Println(s.Name, s.Version, d.Digest)
				}
			} else {
				repo := registry + "/" + ns + "/blueprints/" + s.Name
				d, e := packaging.Push(ctx, packaging.BlueprintLayout(blueprintRoot(root), s.Name, s.Version), s.Version, repo, plain)
				if e != nil {
					failures = append(failures, e)
					fmt.Fprintln(os.Stderr, s.Name, s.Version, e)
				} else {
					fmt.Println(repo + "@" + d.Digest.String())
				}
			}
		}
		return errors.Join(failures...)
	case "bundle:push":
		lock, e := os.ReadFile(filepath.Join(root, "releases/bundles", version+".lock.yaml"))
		if e != nil {
			return e
		}
		recordBytes, e := os.ReadFile(filepath.Join(env("KIT_DIST_DIR", filepath.Join(root, "dist")), "bundles", version, "build.json"))
		if e != nil {
			return e
		}
		var record struct {
			LockSHA256 string `json:"lock_sha256"`
		}
		if e = json.Unmarshal(recordBytes, &record); e != nil {
			return e
		}
		sum := sha256.Sum256(lock)
		if record.LockSHA256 != hex.EncodeToString(sum[:]) {
			return errors.New("bundle lock differs from build record; build the intended version before push")
		}
		repo := registry + "/" + ns + "/runtime"
		d, e := packaging.Push(ctx, filepath.Join(env("KIT_DIST_DIR", filepath.Join(root, "dist")), "bundles", version, "oci"), version, repo, plain)
		if e != nil {
			return e
		}
		fmt.Println(repo + "@" + d.Digest.String())
		return nil
	}
	return errors.New("unknown build action")
}

// Test fixtures use the same packaging contract with an explicit source root.
func blueprintRoot(root string) string { return env("BLUEPRINT_ROOT", root) }
