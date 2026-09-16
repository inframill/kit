package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/inframill/kit/internal/artifacts"
	"github.com/inframill/kit/internal/toolrun"
	driver "github.com/sir-hassan/inframill/pkg/driver"
)

type Plan struct {
	Schema        int            `json:"schema"`
	Binding       driver.Binding `json:"binding"`
	ProducerID    string         `json:"producer_id"`
	DriverVersion string         `json:"driver_version"`
	ToolVersion   string         `json:"tool_version"`
	ConfigHash    string         `json:"config_hash"`
	SourceHash    string         `json:"source_hash"`
	Digest        string         `json:"digest"`
	CommandID     string         `json:"command_id"`
	Mode          string         `json:"mode"`
}
type PlanResult struct {
	PlanID      string `json:"plan_id"`
	Changes     bool   `json:"changes"`
	Destroy     bool   `json:"destroy"`
	RefreshOnly bool   `json:"refresh_only"`
}
type OutputResult struct {
	Artifact string `json:"artifact"`
	Nodes    int    `json:"nodes"`
	Valid    bool   `json:"valid"`
}

func (p *prepared) configHash() string {
	return artifacts.JSONHash(struct {
		Manifest Manifest
		Settings Settings
	}{p.manifest, p.settings})
}
func (p *prepared) planName(id string) string { return p.manifest.ID + "-plan-" + id }
func (p *prepared) savedPlan() (Plan, []byte, error) {
	var rec Plan
	name := p.planName(p.arguments.PlanID)
	data, err := artifacts.Read(p.scope.ArtifactDirectory, name+".json", artifacts.SettingsLimit)
	if err != nil {
		return rec, nil, err
	}
	if err = artifacts.Decode(data, &rec); err != nil {
		return rec, nil, err
	}
	source, err := artifacts.SourceHash(p.scope.WorkspaceDirectory)
	if err != nil {
		return rec, nil, err
	}
	modeOK := p.command == "destroy" && rec.Mode == "destroy" || p.command == "apply" && (rec.Mode == "apply" || rec.Mode == "refresh-only")
	if rec.Schema != 1 || rec.Binding != p.scope.Binding || rec.ProducerID != p.manifest.ID || rec.CommandID != p.arguments.PlanID || rec.DriverVersion != p.driver.Version || rec.ToolVersion != p.driver.ToolVersion || rec.ConfigHash != p.configHash() || rec.SourceHash != source || !modeOK {
		return rec, nil, &driver.Error{Code: "INCOMPATIBLE_PLAN", Message: "saved plan no longer matches this command; create a fresh plan"}
	}
	data, err = artifacts.Read(p.scope.ArtifactDirectory, name+".bin", artifacts.Limit)
	if err != nil {
		return rec, nil, err
	}
	if artifacts.Hash(data) != rec.Digest {
		return rec, nil, errors.New("saved plan integrity mismatch")
	}
	return rec, data, nil
}
func (p *prepared) invalidate() error {
	return artifacts.Write(p.scope.ArtifactDirectory, p.manifest.Artifact, artifacts.Nodes{Schema: 1, Valid: false, Binding: p.scope.Binding, ProducerID: p.manifest.ID, ProducerVersion: p.driver.Version, CommandID: p.scope.CommandID})
}
func (p *prepared) execute(ctx context.Context, run toolrun.Runner) (any, error) {
	switch p.command {
	case "init":
		args := []string{"init", "-input=false", "-no-color", "-lockfile=readonly"}
		if p.manifest.Backend == "s3" {
			args = append(args, "-backend-config=key=instances/"+p.scope.Binding.InstanceID+"/terraform.tfstate")
		}
		keys := []string{}
		for key := range p.settings.Backend {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, fmt.Sprintf("-backend-config=%s=%v", key, p.settings.Backend[key]))
		}
		_, code, err := run.Call(ctx, false, args...)
		return struct {
			Initialized bool `json:"initialized"`
		}{true}, toolrun.Success(code, err)
	case "validate":
		_, code, err := run.Call(ctx, false, "validate", "-no-color")
		return struct {
			Valid bool `json:"valid"`
		}{true}, toolrun.Success(code, err)
	case "plan":
		return p.plan(ctx, run)
	case "apply", "destroy":
		return p.apply(ctx, run)
	case "output":
		if err := p.invalidate(); err != nil {
			return nil, err
		}
		return p.publish(ctx, run)
	}
	return nil, errors.New("unknown command")
}
func (p *prepared) plan(ctx context.Context, run toolrun.Runner) (any, error) {
	if !driver.ValidCommandID(p.scope.CommandID) {
		return nil, errors.New("command id required")
	}
	tmp, err := os.MkdirTemp(p.scope.WorkspaceDirectory, ".kit-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	args := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-lock-timeout=60s", "-out=" + filepath.Join(tmp, "plan.bin")}
	mode := "apply"
	if p.arguments.Destroy {
		args = append(args, "-destroy")
		mode = "destroy"
	}
	if p.arguments.RefreshOnly {
		args = append(args, "-refresh-only")
		mode = "refresh-only"
	}
	_, code, err := run.Call(ctx, false, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 && code != 2 {
		return nil, &driver.Error{Code: "TOOL_FAILED", Message: "OpenTofu plan failed"}
	}
	data, err := artifacts.Read(tmp, "plan.bin", artifacts.Limit)
	if err != nil {
		return nil, err
	}
	source, err := artifacts.SourceHash(p.scope.WorkspaceDirectory)
	if err != nil {
		return nil, err
	}
	rec := Plan{Schema: 1, Binding: p.scope.Binding, ProducerID: p.manifest.ID, DriverVersion: p.driver.Version, ToolVersion: p.driver.ToolVersion, ConfigHash: p.configHash(), SourceHash: source, Digest: artifacts.Hash(data), CommandID: p.scope.CommandID, Mode: mode}
	if err = toolrun.Public(rec, run.Secrets); err != nil {
		return nil, err
	}
	name := p.planName(p.scope.CommandID)
	if err = artifacts.WriteBytes(p.scope.ArtifactDirectory, name+".bin", data); err != nil {
		return nil, err
	}
	if err = artifacts.Write(p.scope.ArtifactDirectory, name+".json", rec); err != nil {
		_ = artifacts.Remove(p.scope.ArtifactDirectory, name+".bin")
		return nil, err
	}
	return PlanResult{p.scope.CommandID, code == 2, p.arguments.Destroy, p.arguments.RefreshOnly}, nil
}
func (p *prepared) apply(ctx context.Context, run toolrun.Runner) (any, error) {
	_, data, err := p.savedPlan()
	if err != nil {
		return nil, err
	}
	if err = p.invalidate(); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(p.scope.WorkspaceDirectory, ".kit-apply-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err = artifacts.WriteBytes(tmp, "plan.bin", data); err != nil {
		return nil, err
	}
	// Retire the admission record before effects: failed/partial application requires a fresh plan.
	name := p.planName(p.arguments.PlanID)
	if err = artifacts.Remove(p.scope.ArtifactDirectory, name+".json"); err != nil {
		return nil, err
	}
	if err = artifacts.Remove(p.scope.ArtifactDirectory, name+".bin"); err != nil {
		return nil, err
	}
	_, code, err := run.Call(ctx, false, "apply", "-input=false", "-no-color", "-lock-timeout=60s", filepath.Join(tmp, "plan.bin"))
	if err = toolrun.Success(code, err); err != nil {
		return nil, err
	}
	if p.command == "destroy" {
		data, code, err := run.Call(ctx, true, "show", "-json")
		if err = toolrun.Success(code, err); err != nil {
			return nil, err
		}
		var state struct {
			Values struct {
				Root module `json:"root_module"`
			} `json:"values"`
		}
		if json.Unmarshal(data, &state) != nil {
			return nil, errors.New("invalid state response")
		}
		if managed(state.Values.Root) > 0 {
			return nil, errors.New("managed resources remain")
		}
		return struct {
			Destroyed bool `json:"destroyed"`
			Empty     bool `json:"managed_state_empty"`
		}{true, true}, nil
	}
	return p.publish(ctx, run)
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
func (p *prepared) publish(ctx context.Context, run toolrun.Runner) (any, error) {
	data, code, err := run.Call(ctx, true, "output", "-json", p.manifest.NodeOutput)
	if err = toolrun.Success(code, err); err != nil {
		return nil, err
	}
	var nodes map[string]artifacts.Node
	if err = artifacts.Decode(data, &nodes); err != nil {
		return nil, err
	}
	if len(nodes) == 0 || len(nodes) > 1000 {
		return nil, errors.New("node output must contain 1..1000 nodes")
	}
	record := artifacts.Nodes{Schema: 1, Valid: true, Binding: p.scope.Binding, ProducerID: p.manifest.ID, ProducerVersion: p.driver.Version, CommandID: p.scope.CommandID}
	keys := []string{}
	for key := range nodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		node := nodes[key]
		if node.Name != key {
			return nil, errors.New("node key differs from name")
		}
		if err = artifacts.CheckNode(node); err != nil {
			return nil, err
		}
		record.Nodes = append(record.Nodes, node)
	}
	if err = toolrun.Public(record, run.Secrets); err != nil {
		return nil, err
	}
	if err = artifacts.Write(p.scope.ArtifactDirectory, p.manifest.Artifact, record); err != nil {
		return nil, err
	}
	return OutputResult{p.manifest.Artifact, len(nodes), true}, nil
}
