package localexec

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

type resultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type result struct {
	Command    string               `json:"command,omitempty"`
	Arguments  inframill.Arguments  `json:"args,omitempty"`
	CreatedAt  *time.Time           `json:"created_at,omitempty"`
	StartedAt  *time.Time           `json:"started_at,omitempty"`
	EndedAt    *time.Time           `json:"ended_at,omitempty"`
	APIVersion string               `json:"api_version"`
	InstanceID inframill.InstanceID `json:"instance_id,omitempty"`
	CommandID  inframill.CommandID  `json:"command_id,omitempty"`
	Status     string               `json:"status"`
	Result     json.RawMessage      `json:"result,omitempty"`
	Error      *resultError         `json:"error,omitempty"`
}

func commandResult(id inframill.InstanceID, c inframill.CommandExecution) result {
	r := result{APIVersion: resultVersion, Command: c.Request.Command, Arguments: c.Request.Args, CreatedAt: &c.CreatedAt, StartedAt: c.StartedAt, EndedAt: c.EndedAt, InstanceID: id, CommandID: c.Request.ID, Status: string(c.Status), Result: c.Result}
	if c.Error != nil {
		r.Error = &resultError{Code: string(c.Error.Code), Message: c.Error.Message, Path: c.Error.Path}
	}
	return r
}

// resultFile reserves a new destination before effects. Only our empty reserved
// file can be replaced; complete JSON is synced in a sibling staging file first.
type resultFile struct {
	path     string
	reserved os.FileInfo
}

func reserveResult(o options) (*resultFile, error) {
	if o.resultFile == "" {
		return nil, nil //nolint:nilnil // No result destination was requested.
	}
	absolute, err := filepath.Abs(o.resultFile)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, errors.New("result parent directory must exist")
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	for _, input := range []string{o.instance, o.argsFile, o.envFile, o.config} {
		if input == "" {
			continue
		}
		path, pathErr := filepath.Abs(input)
		if pathErr != nil {
			return nil, pathErr
		}
		if dir, resolveErr := filepath.EvalSymlinks(filepath.Dir(path)); resolveErr == nil {
			path = filepath.Join(dir, filepath.Base(path))
		}
		if path == absolute {
			return nil, errors.New("result file must differ from input files")
		}
	}
	for _, directory := range []string{o.workspace, o.blueprint} {
		if directory == "" {
			continue
		}
		root, pathErr := filepath.Abs(directory)
		if pathErr != nil {
			return nil, pathErr
		}
		if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
			root = resolved
		}
		rel, relErr := filepath.Rel(root, absolute)
		if relErr != nil {
			return nil, relErr
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return nil, errors.New("result file must be outside Blueprint and workspace")
		}
	}
	//nolint:gosec // Explicit new result destination; exclusive create prevents overwriting input.
	f, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errors.New("result file must be a new writable file")
	}
	info, statErr := f.Stat()
	if err := errors.Join(statErr, f.Close()); err != nil {
		return nil, err
	}
	return &resultFile{path: absolute, reserved: info}, nil
}

func (f *resultFile) publish(r result) error {
	if f == nil {
		return nil
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	staging, err := os.CreateTemp(filepath.Dir(f.path), ".local-result-")
	if err != nil {
		return err
	}
	defer os.Remove(staging.Name()) //nolint:errcheck // Only our unpublished staging file.
	_, writeErr := staging.Write(data)
	if flushErr := errors.Join(writeErr, staging.Sync(), staging.Close()); flushErr != nil {
		return flushErr
	}
	current, err := os.Lstat(f.path)
	if err != nil || !os.SameFile(current, f.reserved) || !current.Mode().IsRegular() || current.Size() != 0 {
		return errors.New("reserved result file changed before publication")
	}
	if renameErr := os.Rename(staging.Name(), f.path); renameErr != nil {
		return renameErr
	}
	dir, err := os.Open(filepath.Dir(f.path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
