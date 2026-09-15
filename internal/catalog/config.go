package catalog

// Configuration centralizes aliases and executable arguments for local and image use.
func Configuration(executable, dataDir string, selected []Release, tools map[string]string) map[string]any {
	drivers := map[string]any{}
	for _, r := range selected {
		args := []string{"driver", r.Name, "--driver-version", r.Version}
		if tool := tools[r.Name]; tool != "" {
			args = append(args, "--tool", tool)
		}
		drivers[ConfiguredID(r.Name, r.Version)] = map[string]any{"path": executable, "args": args}
	}
	return map[string]any{"engine": map[string]any{"dataDir": dataDir, "drivers": drivers}, "handlers": []any{map[string]any{"type": "connect-engine", "settings": map[string]any{"path": "/inframill.v1.EngineService/"}}}}
}
