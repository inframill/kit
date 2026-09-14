package driverkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/inframill/kit/internal/catalog"
	wire "github.com/sir-hassan/inframill/pkg/driver"
)

type planRecord struct {
	Schema        int          `json:"schema"`
	Binding       wire.Binding `json:"binding"`
	DriverVersion string       `json:"driver_version"`
	ToolVersion   string       `json:"tool_version"`
	ConfigHash    string       `json:"config_hash"`
	SourceHash    string       `json:"source_hash"`
	Digest        string       `json:"digest"`
	CommandID     string       `json:"command_id"`
	Destroy       bool         `json:"destroy"`
}

func (r runner) invalidate(c Config, req wire.Request) error {
	return writePrivate(req.ArtifactDirectory, c.Artifact, map[string]any{"schema": 1, "valid": false})
}
func (r runner) tofu(ctx context.Context, release catalog.Release, c Config, a Arguments, req wire.Request) (any, error) {
	switch req.Command {
	case "init":
		args := []string{"init", "-input=false", "-no-color", "-lockfile=readonly"}
		if c.Backend == "s3" {
			args = append(args, "-backend-config=key=instances/"+req.Binding.InstanceID+"/terraform.tfstate")
		}
		keys := make([]string, 0, len(c.BackendConfig))
		for k := range c.BackendConfig {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, e := c.BackendConfig[k].resolve(req.Values)
			if e != nil {
				return nil, e
			}
			args = append(args, "-backend-config="+k+"="+v)
		}
		_, code, e := r.call(ctx, false, args...)
		return map[string]bool{"initialized": true}, toolSuccess(code, e)
	case "validate":
		_, code, e := r.call(ctx, false, "validate", "-no-color")
		return map[string]bool{"valid": true}, toolSuccess(code, e)
	case "plan":
		// Private workspace directory, never the shared public output projection.
		tmp, e := os.MkdirTemp(req.WorkspaceDirectory, ".kit-plan-")
		if e != nil {
			return nil, e
		}
		defer os.RemoveAll(tmp)
		plan := filepath.Join(tmp, "plan.bin")
		args := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-lock-timeout=60s", "-out=" + plan}
		if a.Destroy {
			args = append(args, "-destroy")
		}
		if a.RefreshOnly {
			args = append(args, "-refresh-only")
		}
		_, code, e := r.call(ctx, false, args...)
		if e != nil {
			return nil, e
		}
		if code != 0 && code != 2 {
			return nil, fmt.Errorf("OpenTofu plan failed with status %d", code)
		}
		b, e := readPrivate(tmp, "plan.bin", maxArtifact)
		if e != nil {
			return nil, e
		}
		source, e := sourceHash(req.WorkspaceDirectory)
		if e != nil {
			return nil, e
		}
		name := c.ID + "-plan-" + req.CommandID
		rec := planRecord{1, *req.Binding, release.Version, release.ToolVersion, jsonHash(c), source, hash(b), req.CommandID, a.Destroy}
		if e = writeBytes(req.ArtifactDirectory, name+".bin", b); e != nil {
			return nil, e
		}
		if e = writePrivate(req.ArtifactDirectory, name+".json", rec); e != nil {
			return nil, e
		}
		return map[string]any{"plan_id": req.CommandID, "changes": code == 2, "destroy": a.Destroy, "refresh_only": a.RefreshOnly}, nil
	case "apply", "destroy":
		name := c.ID + "-plan-" + a.PlanID
		b, e := readPrivate(req.ArtifactDirectory, name+".json", maxArtifact)
		if e != nil {
			return nil, e
		}
		var rec planRecord
		if e = decode(b, &rec); e != nil {
			return nil, e
		}
		source, e := sourceHash(req.WorkspaceDirectory)
		if e != nil {
			return nil, e
		}
		if rec.Schema != 1 || rec.Binding != *req.Binding || rec.CommandID != a.PlanID || rec.DriverVersion != release.Version || rec.ToolVersion != release.ToolVersion || rec.ConfigHash != jsonHash(c) || rec.SourceHash != source || rec.Destroy != (req.Command == "destroy") {
			return nil, errors.New("saved plan is incompatible; create a fresh plan")
		}
		raw, e := readPrivate(req.ArtifactDirectory, name+".bin", maxArtifact)
		if e != nil {
			return nil, e
		}
		if hash(raw) != rec.Digest {
			return nil, errors.New("saved plan integrity mismatch")
		}
		if e = r.invalidate(c, req); e != nil {
			return nil, e
		}
		tmp, e := os.MkdirTemp(req.WorkspaceDirectory, ".kit-apply-")
		if e != nil {
			return nil, e
		}
		defer os.RemoveAll(tmp)
		if e = writeBytes(tmp, "plan.bin", raw); e != nil {
			return nil, e
		}
		_, code, e := r.call(ctx, false, "apply", "-input=false", "-no-color", "-lock-timeout=60s", filepath.Join(tmp, "plan.bin"))
		if e = toolSuccess(code, e); e != nil {
			return nil, e
		}
		// A consumed plan cannot be replayed through Kit even if the tool would allow it.
		root, e := os.OpenRoot(req.ArtifactDirectory)
		if e != nil {
			return nil, e
		}
		defer root.Close()
		if e = root.Remove(name + ".json"); e != nil {
			return nil, e
		}
		if e = root.Remove(name + ".bin"); e != nil {
			return nil, e
		}
		if req.Command == "destroy" {
			b, code, e := r.call(ctx, true, "show", "-json")
			if e = toolSuccess(code, e); e != nil {
				return nil, e
			}
			var state struct {
				Values struct {
					Root module `json:"root_module"`
				} `json:"values"`
			}
			if e = json.Unmarshal(b, &state); e != nil {
				return nil, e
			}
			if managed(state.Values.Root) > 0 {
				return nil, errors.New("managed resources remain in state")
			}
			return map[string]bool{"destroyed": true, "managed_state_empty": true}, nil
		}
		return r.publish(ctx, release, c, req)
	case "output":
		if e := r.invalidate(c, req); e != nil {
			return nil, e
		}
		return r.publish(ctx, release, c, req)
	}
	return nil, errors.New("unknown OpenTofu command")
}

type module struct {
	Resources []struct {
		Mode string `json:"mode"`
	} `json:"resources"`
	Children []module `json:"child_modules"`
}

func managed(m module) int {
	n := 0
	for _, r := range m.Resources {
		if r.Mode == "managed" {
			n++
		}
	}
	for _, c := range m.Children {
		n += managed(c)
	}
	return n
}
func (r runner) publish(ctx context.Context, release catalog.Release, c Config, req wire.Request) (any, error) {
	b, code, e := r.call(ctx, true, "output", "-json", c.NodeOutput)
	if e = toolSuccess(code, e); e != nil {
		return nil, e
	}
	var nodes map[string]Node
	if e = decode(b, &nodes); e != nil {
		return nil, e
	}
	if len(nodes) == 0 || len(nodes) > 1000 {
		return nil, errors.New("node output must contain 1..1000 nodes")
	}
	artifact := Nodes{Schema: 1, Valid: true, Binding: *req.Binding, ProducerID: c.ID, ProducerName: release.Name, ProducerVersion: release.Version, CommandID: req.CommandID}
	keys := make([]string, 0, len(nodes))
	for k := range nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		n := nodes[key]
		if n.Name != key {
			return nil, errors.New("node key/name mismatch")
		}
		if e = checkNode(n); e != nil {
			return nil, e
		}
		artifact.Nodes = append(artifact.Nodes, n)
	}
	if e = writePrivate(req.ArtifactDirectory, c.Artifact, artifact); e != nil {
		return nil, e
	}
	return map[string]any{"artifact": c.Artifact, "nodes": len(nodes), "valid": true}, nil
}
