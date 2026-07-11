package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCmd prints the CLI version. It preserves the pre-cobra behavior
// (`lotsman version` -> "lotsman <version>") and the ldflags wiring: the value
// comes from package-level main.version, stamped via -X main.version=... .
func newVersionCmd(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.output == outputJSON {
				return renderJSON(cmd.OutOrStdout(),
					[]byte(fmt.Sprintf(`{"version":%q}`, version)))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "lotsman %s\n", version)
			return nil
		},
	}
}
