// Package backend implements local workspaces and private per-Instance execution.
package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"

	"github.com/sir-hassan/inframill/internal/backend/render"
	"github.com/sir-hassan/inframill/internal/blueprint/content"
	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const deleteCommand = "builtin.delete"

type Options struct {
	QueueCapacity int
	Clock         inframill.Clock
	// Optional live sinks receive already-redacted command output after log writes.
	Stdout io.Writer
	Stderr io.Writer
}
type Backend struct {
	root           string
	drivers        drivers.Manager
	capacity       int
	stdout         io.Writer
	stderr         io.Writer
	clock          inframill.Clock
	mu             sync.Mutex
	initialization sync.Mutex
	active         map[inframill.InstanceID]*instance
}

var _ inframill.Backend = (*Backend)(nil)

func New(root string, supplied drivers.Manager, options Options) (*Backend, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if inframill.IsNil(supplied) {
		return nil, errors.New("drivers are required")
	}
	if options.QueueCapacity == 0 {
		options.QueueCapacity = 64
	}
	if options.QueueCapacity < 1 {
		return nil, errors.New("queue capacity must be positive")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &Backend{root: absolute, drivers: supplied, capacity: options.QueueCapacity, stdout: options.Stdout, stderr: options.Stderr, clock: options.Clock, active: map[inframill.InstanceID]*instance{}}, nil
}

func (b *Backend) ListCommandDefinitions() []inframill.CommandDefinition {
	out := driverCommands(b.drivers)
	out = append(out, inframill.CommandDefinition{Name: deleteCommand, Description: "Delete the local Instance and its history", ArgumentsSchema: map[string]any{"type": "object", "additionalProperties": false}, Safety: inframill.Destructive, Effects: []string{"removes all local Instance files and command history"}, Verification: "Instance absence confirms success"})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type manifestDocument struct {
	APIVersion  string                    `yaml:"api_version"`
	Description string                    `yaml:"description"`
	Drivers     map[string]drivers.Config `yaml:"drivers"`
}

func loadManifest(root string) (map[string]drivers.Config, error) {
	data, err := readRegular(root, "manifest.yaml", 1<<20)
	if err != nil {
		return nil, err
	}
	var doc manifestDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return nil, errors.New("invalid execution manifest")
	}
	if doc.APIVersion != "inframill.dev/blueprint/v1" || len(doc.Drivers) == 0 {
		return nil, errors.New("unsupported or empty execution manifest")
	}
	return doc.Drivers, nil
}

func binding(info inframill.InstanceInfo) drivers.Binding {
	//nolint:musttag,errcheck,errchkjson // Normalized JSON data: hash the neutral in-memory identity and Values, not a stored document.
	data, _ := json.Marshal(struct {
		ID        inframill.InstanceID
		Name      string
		Blueprint string
		Values    inframill.InstanceValues
	}{info.ID, info.Name, info.Blueprint, info.Values})
	sum := sha256.Sum256(data)
	repository, _, _ := strings.Cut(info.Blueprint, "@")
	_, repository, _ = strings.Cut(repository, "/")
	return drivers.Binding{InstanceID: string(info.ID), Name: info.Name, Blueprint: repository, BlueprintReference: info.Blueprint, Digest: hex.EncodeToString(sum[:])}
}

func (b *Backend) InitInstance(ctx context.Context, id inframill.InstanceID, name string, blueprint inframill.Blueprint, values inframill.InstanceValues) error {
	if !inframill.ValidUUID(string(id)) || !inframill.ValidInstanceName(name) {
		return inframill.InvalidArgument("instance", "invalid ID or name")
	}
	if values == nil {
		return inframill.MissingArgument("values")
	}
	values, checkErr := inframill.Normalize(values)
	if checkErr != nil {
		return inframill.InvalidArgument("values", "values must contain JSON data")
	}
	if blueprint.Directory == "" || blueprint.Reference == "" {
		return inframill.MissingArgument("blueprint")
	}
	b.initialization.Lock()
	defer b.initialization.Unlock()
	if validationErr := ctx.Err(); validationErr != nil {
		return validationErr
	}
	if err := os.MkdirAll(b.root, 0o700); err != nil {
		return err
	}
	if err := directory(b.root); err != nil {
		return err
	}
	lock, checkErr := b.lockNamespace(ctx)
	if checkErr != nil {
		return checkErr
	}
	defer lock.Close()
	namespace, checkErr := os.OpenRoot(b.root)
	if checkErr != nil {
		return checkErr
	}
	defer namespace.Close()
	namespaceInfo, checkErr := namespace.Stat(".")
	if checkErr != nil {
		return checkErr
	}
	stored, checkErr := b.discover(ctx)
	if checkErr != nil {
		return checkErr
	}
	for _, existing := range stored {
		if existing.info.ID == id || existing.info.Name == name {
			return inframill.ErrAlreadyExists
		}
		if _, err := readInfo(existing.root, existing.info.ID); err != nil {
			return stateError("read existing Instance identity", err)
		}
	}
	files, checkErr := content.Snapshot(blueprint.Directory)
	if checkErr != nil {
		return checkErr
	}
	staging, checkErr := os.MkdirTemp(b.root, ".init-")
	if checkErr != nil {
		return checkErr
	}
	defer namespace.RemoveAll(filepath.Base(staging)) //nolint:errcheck // Only newly created unpublished staging data.
	destination := filepath.Join(staging, string(id))
	if err := render.Render(ctx, files, values, destination); err != nil {
		return err
	}
	info := inframill.InstanceInfo{ID: id, Name: name, Values: values, Blueprint: blueprint.Reference, Status: inframill.InstanceIdle}
	configs, checkErr := loadManifest(destination)
	if checkErr != nil {
		return checkErr
	}
	if err := b.drivers.Validate(ctx, drivers.ValidationRequest{BlueprintDirectory: destination, Drivers: configs, Binding: bindingPointer(info), Values: values}); err != nil {
		return err
	}
	for _, dir := range []string{"logs", "artifacts"} {
		if err := os.Mkdir(filepath.Join(destination, dir), 0o700); err != nil {
			return err
		}
	}
	store, checkErr := openStore(ctx, destination, true)
	if checkErr != nil {
		return checkErr
	}
	if validationErr := store.db.Close(); validationErr != nil {
		return validationErr
	}
	if err := writeInfo(destination, info); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sameDirectory(b.root, namespaceInfo); err != nil {
		return err
	}
	target := string(id)
	if _, err := namespace.Lstat(target); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return inframill.ErrAlreadyExists
	}
	if err := namespace.Rename(filepath.Join(filepath.Base(staging), target), target); err != nil {
		return err
	}
	parent, err := namespace.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(parent.Sync(), parent.Close())
}

func (b *Backend) SpawnInstance(ctx context.Context, id inframill.InstanceID) (inframill.Instance, error) {
	if !inframill.ValidUUID(string(id)) {
		return nil, inframill.InvalidArgument("instance_id", "instance_id must be a UUID")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if old := b.active[id]; old != nil {
		select {
		case <-old.finished:
			delete(b.active, id)
		default:
			old.mu.Lock()
			err := old.checkIdentity()
			if err != nil {
				old.failLocked(err)
			}
			old.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return old, nil
		}
	}
	root, lookupErr := b.instancePath(ctx, id)
	if lookupErr != nil {
		return nil, lookupErr
	}
	if err := directory(root); err != nil {
		return nil, err
	}
	info, readErr := readInfo(root, id)
	if info.ID == "" {
		return nil, readErr
	}
	if filepath.Join(b.root, string(id)) != root {
		return nil, unsupportedLayout()
	}
	parentIdentity, err := os.Lstat(b.root)
	if err != nil {
		return nil, err
	}
	rootIdentity, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	i := newInstance(b, root, info)
	i.parentIdentity, i.rootIdentity = parentIdentity, rootIdentity
	b.active[id] = i
	fail := func(err error) (inframill.Instance, error) {
		i.failLocked(stateError("start Instance", err))
		i.finish()
		return i, nil
	}
	if readErr != nil {
		return fail(readErr)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := validateCommandStore(ctx, root); err != nil {
		return fail(err)
	}
	lockPath := filepath.Join(root, ".owner.lock")
	if err := regularFile(lockPath); err != nil && !os.IsNotExist(err) {
		return fail(err)
	}
	i.owner = flock.New(lockPath)
	locked, err := i.owner.TryLock()
	if err != nil {
		return fail(err)
	}
	if !locked {
		i.owner = nil
		return fail(errors.New("workspace already has an owner"))
	}
	i.owned = true
	if err := i.checkIdentity(); err != nil {
		return fail(err)
	}
	// The ownership file is private just like metadata and database files.
	if checkErr := os.Chmod(lockPath, 0o600); checkErr != nil {
		return fail(checkErr)
	}
	i.configs, err = loadManifest(root)
	if err != nil {
		return fail(err)
	}
	for _, dir := range []string{"logs", "artifacts", "workspace"} {
		if checkErr := directory(filepath.Join(root, dir)); checkErr != nil {
			return fail(checkErr)
		}
	}
	i.store, err = openStore(ctx, root, false)
	if err != nil {
		return fail(err)
	}
	if err := i.store.interrupt(ctx, b.clock().UTC()); err != nil {
		return fail(err)
	}
	if err := i.publishLocked(inframill.InstanceIdle); err != nil {
		return fail(err)
	}
	i.accepting = true
	go i.work()
	return i, nil
}
