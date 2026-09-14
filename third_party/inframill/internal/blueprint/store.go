package blueprint

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"

	blueprintcontent "github.com/sir-hassan/inframill/internal/blueprint/content"
	"github.com/sir-hassan/inframill/internal/platform/filetree"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const indexVersion = "inframill.dev/packages/v1"

// Store owns one daemon's local OCI packages. Construction does no I/O.
type Store struct {
	root      string
	httpHosts map[string]bool
	mu        sync.Mutex
}

var _ inframill.BlueprintSource = (*Store)(nil)

// NewStore configures the local store and explicit development HTTP registries.
func NewStore(root string, httpHosts []string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Store{root: absolute, httpHosts: map[string]bool{}}

	for _, host := range httpHosts {
		if host == "" {
			continue
		}
		ref, parseErr := parseReference(host + "/test:latest")
		if parseErr != nil || ref.Registry != host {
			return nil, errors.New("invalid development HTTP registry host")
		}
		s.httpHosts[host] = true
	}
	return s, nil
}

type storedBlueprint struct {
	Description string `json:"description"`
	APIVersion  string `json:"api_version"`
}
type storeIndex struct {
	Version  string                     `json:"version"`
	Packages map[string]storedBlueprint `json:"packages"`
	Tags     map[string]string          `json:"tags"`
}

func (s *Store) index() (storeIndex, error) {
	idx := storeIndex{Version: indexVersion, Packages: map[string]storedBlueprint{}, Tags: map[string]string{}}
	content, err := readLocal(s.root, "index.json", 16<<20)
	if os.IsNotExist(err) {
		return idx, nil
	}
	if err != nil {
		return idx, err
	}
	if json.Unmarshal(content, &idx) != nil || idx.Version != indexVersion || idx.Packages == nil || idx.Tags == nil {
		return idx, errors.New("invalid local package index")
	}
	for pin := range idx.Packages {
		ref, parseErr := parseReference(pin)
		if parseErr != nil || !strings.Contains(pin, "@") || ref.Reference == "" {
			return idx, errors.New("invalid package identity in local index")
		}
	}
	for tag, pin := range idx.Tags {
		ref, parseErr := parseReference(tag)
		if parseErr != nil || strings.Contains(tag, "@") || !strings.HasPrefix(pin, ref.Registry+"/"+ref.Repository+"@") {
			return idx, errors.New("invalid local tag mapping")
		}
		if _, ok := idx.Packages[pin]; !ok {
			return idx, errors.New("local tag references missing package")
		}
	}
	return idx, nil
}

func readLocal(root, name string, limit int64) ([]byte, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close() //nolint:errcheck // Read-only handle.
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // Read-only handle.
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid or oversized local package file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("oversized local package file")
	}
	return b, err
}

func (s *Store) Fetch(ctx context.Context, reference string) (inframill.Blueprint, error) {
	ref, err := parseReference(reference)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	a, dgst, err := remoteArchive(ctx, ref, s.httpHosts[ref.Registry])
	if err != nil {
		return inframill.Blueprint{}, err
	}
	pin := ref.Registry + "/" + ref.Repository + "@" + dgst
	snapshot, err := s.compile(ctx, a, pin)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	b := snapshot
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkErr := ctx.Err(); checkErr != nil {
		return inframill.Blueprint{}, checkErr
	}
	idx, err := s.index()
	if err != nil {
		return inframill.Blueprint{}, err
	}
	if checkErr := s.publish(ctx, a, dgst); checkErr != nil {
		return inframill.Blueprint{}, checkErr
	}
	idx.Packages[pin] = storedBlueprint{Description: b.Description, APIVersion: b.APIVersion}
	if !strings.Contains(reference, "@") {
		idx.Tags[ref.Registry+"/"+ref.Repository+":"+ref.Reference] = pin
	}
	content, err := json.Marshal(idx)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	if checkErr := atomicFile(ctx, s.root, "index.json", content); checkErr != nil {
		return inframill.Blueprint{}, checkErr
	}
	return s.blueprintInfo(idx, pin)
}

func (s *Store) compile(ctx context.Context, a Archive, pin string) (inframill.Blueprint, error) {
	_, dgst, _ := strings.Cut(pin, "@")
	files, err := unpack(ctx, a, dgst)
	if err != nil {
		if ctx.Err() != nil {
			return inframill.Blueprint{}, ctx.Err()
		}
		return inframill.Blueprint{}, packageError(inframill.InvalidConfig, "invalid Blueprint package: "+err.Error())
	}
	tmp, err := os.MkdirTemp("", "inframill-package-")
	if err != nil {
		return inframill.Blueprint{}, err
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // Disposable source validation tree.
	root := filepath.Join(tmp, blueprintKey)
	if checkErr := os.Mkdir(root, 0o700); checkErr != nil {
		return inframill.Blueprint{}, checkErr
	}
	for _, name := range sortedKeys(files) {
		if checkErr := ctx.Err(); checkErr != nil {
			return inframill.Blueprint{}, checkErr
		}
		file := files[name]
		p := filepath.Join(root, filepath.FromSlash(name))
		if file.Directory {
			err = os.MkdirAll(p, 0o700)
		} else {
			mode := os.FileMode(0o600)
			if file.Executable {
				mode = 0o700
			}
			err = os.WriteFile(p, file.Content, mode)
		}
		if err != nil {
			return inframill.Blueprint{}, err
		}
	}
	b, err := metadata(root)
	if err != nil {
		return inframill.Blueprint{}, packageError(inframill.InvalidConfig, "invalid Blueprint package layout, metadata or schema")
	}
	ref, err := parseReference(pin)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	b.Repository = ref.Repository
	b.Digest, b.Reference = dgst, pin
	return b, nil
}

func (s *Store) publish(ctx context.Context, a Archive, dgst string) error {
	if checkErr := os.MkdirAll(s.root, 0o700); checkErr != nil {
		return checkErr
	}
	name := strings.TrimPrefix(dgst, "sha256:")
	// Publication verifies existing immutable content before updating tag mappings.
	tmp, err := os.MkdirTemp(s.root, ".fetch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // Disposable staging tree.
	for filename, content := range map[string][]byte{"manifest.json": a.Manifest, "config.json": a.Config, "layer.tar.gz": a.Layer} {
		if checkErr := atomicFile(ctx, tmp, filename, content); checkErr != nil {
			return checkErr
		}
	}

	files, err := unpack(ctx, a, dgst)
	if err != nil {
		return err
	}
	source := filepath.Join(tmp, "source")
	if err := writeSource(ctx, source, files); err != nil {
		return err
	}
	destination := filepath.Join(s.root, name)
	if _, checkErr := os.Stat(destination); checkErr == nil {
		// Immutable content cannot be replaced under existing readers. Verify existing bytes.
		old, readErr := s.archive(dgst)
		if readErr != nil {
			return readErr
		}
		if _, verifyErr := unpack(ctx, old, dgst); verifyErr != nil {
			return verifyErr
		}
		existing, readErr := blueprintcontent.Snapshot(filepath.Join(destination, "source"))
		if readErr != nil {
			return readErr
		}
		packed, packErr := Pack(existing)
		if packErr != nil {
			return packErr
		}
		if descriptor("", packed.Manifest).Digest.String() != dgst {
			return errors.New("extracted Blueprint content changed")
		}
		return nil
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	return os.Rename(tmp, destination)
}

func atomicFile(ctx context.Context, root, name string, content []byte) error {
	f, err := os.CreateTemp(root, ".write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) //nolint:errcheck // Staging file, absent after rename.
	_, writeErr := f.Write(content)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	return os.Rename(f.Name(), filepath.Join(root, name))
}

func (s *Store) archive(dgst string) (Archive, error) {
	d := digest.Digest(dgst)
	if d.Validate() != nil || d.Algorithm() != digest.SHA256 {
		return Archive{}, errors.New("invalid stored package digest")
	}
	var a Archive
	var err error
	a.Manifest, err = readLocal(s.root, d.Encoded()+"/manifest.json", maxManifest)
	if err != nil {
		return a, err
	}
	a.Config, err = readLocal(s.root, d.Encoded()+"/config.json", maxConfig)
	if err != nil {
		return a, err
	}
	a.Layer, err = readLocal(s.root, d.Encoded()+"/layer.tar.gz", maxLayer)
	return a, err
}

func (s *Store) Resolve(ctx context.Context, reference string) (inframill.Blueprint, error) {
	ref, err := parseReference(reference)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkErr := ctx.Err(); checkErr != nil {
		return inframill.Blueprint{}, checkErr
	}
	idx, err := s.index()
	if err != nil {
		return inframill.Blueprint{}, err
	}
	pin := reference
	if !strings.Contains(reference, "@") {
		pin = idx.Tags[ref.Registry+"/"+ref.Repository+":"+ref.Reference]
	}
	if _, exists := idx.Packages[pin]; !exists {
		return inframill.Blueprint{}, packageError(inframill.BlueprintNotFound, "Blueprint is not in the local store; fetch this Blueprint first")
	}
	_, dgst, _ := strings.Cut(pin, "@")
	a, err := s.archive(dgst)
	if err != nil {
		return inframill.Blueprint{}, packageError(inframill.InvalidConfig, "local package is missing or corrupt; restore the package store and fetch again")
	}
	if _, checkErr := unpack(ctx, a, dgst); checkErr != nil {
		if ctx.Err() != nil {
			return inframill.Blueprint{}, ctx.Err()
		}
		return inframill.Blueprint{}, packageError(inframill.InvalidConfig, "local package integrity verification failed")
	}
	info, err := s.blueprintInfo(idx, pin)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	files, err := blueprintcontent.Snapshot(info.Directory)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	archive, err := Pack(files)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	if descriptor("", archive.Manifest).Digest.String() != dgst {
		return inframill.Blueprint{}, errors.New("extracted Blueprint content changed")
	}
	return info, nil
}

func (s *Store) blueprintInfo(idx storeIndex, pin string) (inframill.Blueprint, error) {
	_, dgst, _ := strings.Cut(pin, "@")
	b := idx.Packages[pin]
	ref, err := parseReference(pin)
	if err != nil {
		return inframill.Blueprint{}, err
	}
	info := inframill.Blueprint{Repository: ref.Repository, Reference: pin, Digest: dgst, Description: b.Description, APIVersion: b.APIVersion, Directory: filepath.Join(s.root, strings.TrimPrefix(dgst, "sha256:"), "source"), Tags: []string{}}
	for tag, mapped := range idx.Tags {
		if mapped == pin {
			info.Tags = append(info.Tags, tag)
		}
	}
	sort.Strings(info.Tags)
	return info, nil
}

func (s *Store) List(ctx context.Context) ([]inframill.Blueprint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idx, err := s.index()
	if err != nil {
		return nil, err
	}
	result := []inframill.Blueprint{}
	for _, pin := range sortedKeys(idx.Packages) {
		info, infoErr := s.blueprintInfo(idx, pin)
		if infoErr != nil {
			return nil, infoErr
		}
		result = append(result, info)
	}
	return result, nil
}

func (*Store) ValidateValues(ctx context.Context, b inframill.Blueprint, values inframill.InstanceValues) error {
	return validateValues(ctx, b, values)
}

func writeSource(ctx context.Context, root string, files map[string]filetree.File) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	for _, name := range sortedKeys(files) {
		if err := ctx.Err(); err != nil {
			return err
		}
		file := files[name]
		p := filepath.Join(root, filepath.FromSlash(name))
		if file.Directory {
			if err := os.MkdirAll(p, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if file.Executable {
			mode = 0o700
		}
		if err := os.WriteFile(p, file.Content, mode); err != nil {
			return err
		}
	}
	return nil
}
