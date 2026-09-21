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

	"gopkg.in/yaml.v3"

	"github.com/inframill/kit/internal/catalog"
	"github.com/inframill/kit/internal/packaging"
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
	var version string
	if action == "build" || action == "bundle:push" || action == "bundle:validate" || action == "inframill:copy" {
		version, e = packaging.ProjectVersion(root)
		if e != nil {
			return e
		}
	}
	registry := env("REGISTRY", "ghcr.io")
	ns := env("NAMESPACE", "inframill")
	plain := os.Getenv("PLAIN_HTTP") == "true"
	if !catalog.ValidName(ns) {
		return errors.New("invalid namespace")
	}
	switch action {
	case "build":
		kv := version
		if _, e = packaging.Kit(root, kv); e != nil {
			return e
		}
		if e = os.MkdirAll("bin", 0755); e != nil {
			return e
		}
		return command(root, "go", "build", "-trimpath", "-ldflags=-X main.version="+kv, "-o", "bin/inframill-kit", "./cmd/inframill-kit")
	case "inframill:copy":
		lockBytes, err := packaging.Bundle(root, version)
		if err != nil {
			return err
		}
		var lock packaging.KitRelease
		if err = json.Unmarshal(lockBytes, &lock); err != nil {
			return err
		}
		iv := lock.Inframill.Version
		if !catalog.ValidVersion(iv) {
			return errors.New("invalid Inframill release version")
		}
		entry, ok := lock.Inframill.Binaries[runtime.GOOS+"/"+runtime.GOARCH]
		if !ok || !filepath.IsLocal(entry.Path) {
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
		config := catalog.Configuration(env("KIT_EXECUTABLE", filepath.Join(root, "bin/inframill-kit")), env("DATA_DIR", filepath.Join(root, ".inframill")), selected, tools)
		switch env("CONFIG_FORMAT", "json") {
		case "json":
			return json.NewEncoder(os.Stdout).Encode(config)
		case "yaml":
			encoder := yaml.NewEncoder(os.Stdout)
			encoder.SetIndent(2)
			return encoder.Encode(config)
		default:
			return errors.New("CONFIG_FORMAT must be json or yaml")
		}
	case "bundle:validate":
		_, e := packaging.Bundle(root, version)
		return e
	case "bundle:push":
		lock, e := packaging.Bundle(root, version)
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
		repo := registry + "/" + ns + "/kit"
		d, e := packaging.Push(ctx, filepath.Join(env("KIT_DIST_DIR", filepath.Join(root, "dist")), "bundles", version, "oci"), version, repo, plain)
		if e != nil {
			return e
		}
		fmt.Println(repo + "@" + d.Digest.String())
		return nil
	}
	return errors.New("unknown build action")
}
