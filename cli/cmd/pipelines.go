package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// "Pipeline" is what a project used to be called.
//
// The rename left this command behind, pointed at /api/v1/pipelines -- a route that does not
// exist and, going by the rest of the codebase, never did. It also ignored both the HTTP
// status and the JSON decode error, so every invocation printed "null": indistinguishable
// from an instance that genuinely has no projects.
//
// Kept as a deprecated alias rather than deleted, so a script written against the old name
// keeps working and says what to use instead. The vocabulary is still visible in
// SPECIFICATIONS.md, where namespaces are "my-pipeline-production" -- today's
// {project}-{environment}.

var pipelinesCmd = &cobra.Command{
	Use:        "pipelines",
	Short:      "Deprecated alias for 'project'",
	Deprecated: "pipelines were renamed to projects; use 'vesta project' instead.",
}

var pipelinesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := apiRequest("GET", "/api/v1/projects", nil)
		if err != nil {
			return err
		}

		var result interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			// Reported rather than swallowed. Discarding this error is what turned a 404
			// into the word "null" on stdout.
			return fmt.Errorf("decoding response: %w", err)
		}

		formatted, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, string(formatted))
		return nil
	},
}

func init() {
	pipelinesCmd.AddCommand(pipelinesListCmd)
	rootCmd.AddCommand(pipelinesCmd)
}
