package dispatch

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/inframill/kit/providers/roots/internal/artifacts"
)

const sourceByteLimit = 256 << 20
const sourceEntryLimit = 10000

func validateExcludes(patterns []string) error {
	if len(patterns) > 100 {
		return errors.New("too many exclude patterns")
	}
	for _, pattern := range patterns {
		if len(pattern) > 512 || !artifacts.Relative(pattern) || strings.ContainsAny(pattern, "\r\n") {
			return errors.New("invalid exclude pattern")
		}
		for _, segment := range strings.Split(pattern, "/") {
			if strings.Contains(segment, "**") && segment != "**" {
				return errors.New("double-star excludes must occupy a whole path segment")
			}
			if _, err := path.Match(segment, ""); err != nil {
				return errors.New("invalid exclude glob")
			}
		}
	}
	return nil
}

// Globs are source-relative; ** matches zero or more whole path segments.
func excluded(pattern, name string) bool {
	patternParts, nameParts := strings.Split(pattern, "/"), strings.Split(name, "/")
	type position struct{ pattern, name int }
	seen := map[position]bool{}
	var match func(int, int) bool
	match = func(p, n int) bool {
		key := position{p, n}
		if seen[key] {
			return false
		}
		seen[key] = true
		if p == len(patternParts) {
			return n == len(nameParts)
		}
		if patternParts[p] == "**" {
			return match(p+1, n) || n < len(nameParts) && match(p, n+1)
		}
		if n == len(nameParts) {
			return false
		}
		ok, _ := path.Match(patternParts[p], nameParts[n])
		return ok && match(p+1, n+1)
	}
	return match(0, 0)
}

func walkSource(ctx context.Context, workspace string, directory Directory, visit func(*os.Root, string, fs.FileInfo) error) error {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	// Check the source and every parent before opening its bounded subtree.
	for name := directory.Source; name != "."; name = filepath.Dir(name) {
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source path must contain only directories, without links")
		}
	}
	source, err := root.OpenRoot(directory.Source)
	if err != nil {
		return err
	}
	defer source.Close()
	var total int64
	entries := 0
	return fs.WalkDir(source.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		for _, pattern := range directory.Exclude {
			if excluded(pattern, name) {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("source contains a link or nonregular file")
		}
		entries++
		if !info.IsDir() {
			total += info.Size()
		}
		if entries > sourceEntryLimit || total > sourceByteLimit || !info.IsDir() && info.Size() > artifacts.Limit {
			return errors.New("source exceeds entry, total byte or per-file limits")
		}
		if visit != nil {
			return visit(source, name, info)
		}
		return nil
	})
}

func archiveSource(ctx context.Context, workspace string, directory Directory, output io.Writer) error {
	compressed := gzip.NewWriter(output)
	archive := tar.NewWriter(compressed)
	err := walkSource(ctx, workspace, directory, func(root *os.Root, name string, info fs.FileInfo) error {
		// Make relies on source mtimes to decide when to rebuild remote outputs.
		header := &tar.Header{Name: name, Mode: int64(info.Mode().Perm()), Typeflag: tar.TypeDir, ModTime: info.ModTime(), Format: tar.FormatPAX}
		if info.IsDir() {
			header.Name += "/"
			return archive.WriteHeader(header)
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		current, err := file.Stat()
		if err != nil {
			return err
		}
		if !current.Mode().IsRegular() || !os.SameFile(info, current) || current.Size() != info.Size() {
			return errors.New("source changed while being archived")
		}
		header.Typeflag, header.Size = tar.TypeReg, info.Size()
		if err = archive.WriteHeader(header); err != nil {
			return err
		}
		if _, err = io.CopyN(archive, file, info.Size()); err != nil {
			return err
		}
		var extra [1]byte
		if n, err := file.Read(extra[:]); n != 0 || err != io.EOF {
			return errors.New("source changed while being archived")
		}
		return ctx.Err()
	})
	return errors.Join(err, archive.Close(), compressed.Close())
}
