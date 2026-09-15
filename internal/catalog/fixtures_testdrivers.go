//go:build kit_testdrivers

package catalog

// Register synthetic versions only in the integration test executable.
func init() {
	for _, name := range []string{"opentofu", "ansible"} {
		r, err := Get(name, "0.1.0")
		if err != nil {
			panic(err)
		}
		r.Version = "0.2.0"
		Register(r)
	}
}
