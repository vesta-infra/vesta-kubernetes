package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var logDrainsCmd = &cobra.Command{
	Use:     "drains",
	Aliases: []string{"drain", "log-drains", "logdrains"},
	Short:   "Manage log drains",
	Long: "Log drains ship app logs to an external destination. Scope decides which apps\n" +
		"ship where: a drain with no project covers every app, one with a project covers\n" +
		"that project, and an app ships to every drain whose scope covers it -- so a\n" +
		"project drain adds to the platform one rather than replacing it.",
}

var logDrainsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List log drains",
	Run: func(cmd *cobra.Command, args []string) {
		path := "/api/v1/log-drains"
		if project, _ := cmd.Flags().GetString("project"); project != "" {
			path += "?project=" + project
		}

		raw, err := apiRequest("GET", path, nil)
		if err != nil {
			fail(err)
		}

		var result struct {
			Drains []struct {
				Name             string `json:"name"`
				Type             string `json:"type"`
				Scope            string `json:"scope"`
				Enabled          bool   `json:"enabled"`
				Ready            bool   `json:"ready"`
				Reason           string `json:"reason"`
				RecordsDelivered int64  `json:"recordsDelivered"`
				Errors           int64  `json:"errors"`
			} `json:"drains"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			fail(fmt.Errorf("decoding response: %w", err))
		}

		if len(result.Drains) == 0 {
			fmt.Println("No log drains defined.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tSCOPE\tDELIVERED\tSTATUS")
		for _, d := range result.Drains {
			status := "ok"
			switch {
			case !d.Enabled:
				status = "disabled"
			case !d.Ready:
				// The reason is the useful half: an unreachable host reads very differently
				// from a spec that will not render.
				status = "not ready"
				if d.Reason != "" {
					status = truncate(d.Reason, 44)
				}
			case d.Errors > 0:
				status = fmt.Sprintf("%d errors", d.Errors)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", d.Name, d.Type, d.Scope, d.RecordsDelivered, status)
		}
		w.Flush()
	},
}

var logDrainsGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show one log drain",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		raw, err := apiRequest("GET", "/api/v1/log-drains/"+args[0], nil)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var logDrainsCreateCmd = &cobra.Command{
	Use:   "create -f <file>",
	Short: "Create a log drain from a YAML or JSON file",
	Long: "Create a log drain from a YAML or JSON file, e.g.:\n\n" +
		"  name: central-logging\n  type: loki\n" +
		"  config:\n    host: loki.monitoring.svc\n    port: 3100\n\n" +
		"Scope it by adding project, environment or app. Credentials go under a separate\n" +
		"credentials key and are written to a Secret rather than to the drain:\n\n" +
		"  name: datadog\n  type: datadog\n  config:\n    site: datadoghq.eu\n" +
		"  credentials:\n    apiKey: ...\n\n" +
		"Use \"-\" to read from stdin.",
	Run: func(cmd *cobra.Command, args []string) {
		file, _ := cmd.Flags().GetString("file")
		if file == "" {
			fail(fmt.Errorf("--file is required"))
		}

		var payload map[string]interface{}
		if err := readConfigFile(file, &payload); err != nil {
			fail(err)
		}

		raw, err := apiRequest("POST", "/api/v1/log-drains", payload)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var logDrainsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a log drain",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		raw, err := apiRequest("DELETE", "/api/v1/log-drains/"+args[0], nil)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

func init() {
	logDrainsListCmd.Flags().String("project", "", "Only show drains available to this project")
	logDrainsCreateCmd.Flags().StringP("file", "f", "", "YAML or JSON file describing the drain, or - for stdin")

	logDrainsCmd.AddCommand(logDrainsListCmd, logDrainsGetCmd, logDrainsCreateCmd, logDrainsDeleteCmd)
	rootCmd.AddCommand(logDrainsCmd)
}
