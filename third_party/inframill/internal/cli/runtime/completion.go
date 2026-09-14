package runtime

import (
	"errors"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Choices gives fixed flag values offline completion and parse-time validation.
func Choices(cmd *cobra.Command, flags *pflag.FlagSet, name, initial, help string, target *string, values ...string) {
	*target = initial
	flags.Var(&choiceValue{target: target, values: values}, name, help)
	Complete(cmd, name, values...)
}

type choiceValue struct {
	target *string
	values []string
}

func (c *choiceValue) String() string { return *c.target }
func (*choiceValue) Type() string     { return "string" }
func (c *choiceValue) Set(value string) error {
	if slices.Contains(c.values, value) {
		*c.target = value
		return nil
	}
	return errors.New("unsupported value")
}

// Complete registers known flag metadata; a duplicate is a construction defect.
func Complete(cmd *cobra.Command, name string, values ...string) {
	if err := cmd.RegisterFlagCompletionFunc(name, cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp)); err != nil {
		panic("duplicate CLI completion registration")
	}
}

func CompleteFiles(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		name := flag.Name
		switch {
		case name == "workspace" || name == "blueprint-dir":
			if err := cmd.MarkFlagDirname(name); err != nil {
				panic("missing CLI directory flag")
			}
		case name == "file" || name == "config" || name == "instance" || strings.HasSuffix(name, "-file"):
			if err := cmd.MarkFlagFilename(name); err != nil {
				panic("missing CLI file flag")
			}
		}
	})
}
