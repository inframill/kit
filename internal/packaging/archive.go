package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"github.com/inframill/kit/internal/artifacts"
	"io/fs"
	"path/filepath"
	"strings"
)

// Archive emits the consumer's canonical source layer from a private snapshot.
// Traversal order, header fields and gzip metadata are independent of host times.
func Archive(directory string) ([]byte, error) {
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	entries := 0
	var expanded int64
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if !fs.ValidPath(relative) || strings.ContainsAny(relative, "\\\x00") {
			return errors.New("invalid blueprint source path")
		}
		if relative != "manifest.yaml" && relative != "schema.json" && relative != "template" && !strings.HasPrefix(relative, "template/") {
			return errors.New("blueprint contains files outside its source contract")
		}
		for _, part := range strings.Split(relative, "/") {
			if forbidden(part) {
				return errors.New("blueprint contains runtime or credential files")
			}
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("blueprint links and special files are forbidden")
		}
		entries++
		if entries > 10000 {
			return errors.New("too many source entries")
		}
		header := &tar.Header{Name: relative, Mode: 0644, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		var content []byte
		if info.IsDir() {
			header.Typeflag = tar.TypeDir
			header.Mode = 0755
		} else {
			content, err = artifacts.Read(directory, filepath.FromSlash(relative), 64<<20)
			if err != nil {
				return err
			}
			header.Size = int64(len(content))
			if info.Mode()&0111 != 0 {
				header.Mode = 0755
			}
		}
		expanded += 512 + ((header.Size+511)/512)*512
		if expanded > 250<<20 {
			return errors.New("source exceeds consumer extracted limit")
		}
		if err = archive.WriteHeader(header); err != nil {
			return err
		}
		if _, err = archive.Write(content); err != nil {
			return err
		}
		if output.Len() > 64<<20 {
			return errors.New("compressed source exceeds consumer limit")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = archive.Close(); err != nil {
		return nil, err
	}
	if err = compressed.Close(); err != nil {
		return nil, err
	}
	if output.Len() > 64<<20 {
		return nil, errors.New("compressed source exceeds consumer limit")
	}
	return output.Bytes(), nil
}
func forbidden(name string) bool {
	for _, reserved := range []string{".terraform", ".inframill", "runtime", ".git"} {
		if name == reserved {
			return true
		}
	}
	if strings.HasPrefix(name, ".env") || strings.HasPrefix(name, ".kit-") {
		return true
	}
	for _, suffix := range []string{".tfstate", ".tfplan", ".tfvars", ".tfvars.json", ".pem", ".key"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
