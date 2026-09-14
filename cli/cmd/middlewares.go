package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

var middlewaresCmd = &cobra.Command{
	Use:     "middlewares",
	Aliases: []string{"middleware", "mw"},
	Short:   "Manage reusable ingress middlewares",
	Long: "Middlewares are ingress policy -- rate limits, basic auth, IP allow lists,\n" +
		"headers -- defined once and attached to any number of app environments.\n\n" +
		"Attachment is ordered, and the order is semantic: Traefik runs middlewares in\n" +
		"sequence, so an allow list above an auth check rejects unknown addresses without\n" +
		"prompting them for a password.",
}

var middlewaresListCmd = &cobra.Command{
	Use:   "list",
	Short: "List middlewares",
	Run: func(cmd *cobra.Command, args []string) {
		path := "/api/v1/middlewares"
		if project, _ := cmd.Flags().GetString("project"); project != "" {
			path += "?project=" + project
		}

		raw, err := apiRequest("GET", path, nil)
		if err != nil {
			fail(err)
		}

		var result struct {
			Middlewares []struct {
				Name         string `json:"name"`
				Type         string `json:"type"`
				Description  string `json:"description"`
				Project      string `json:"project"`
				Ready        bool   `json:"ready"`
				Reason       string `json:"reason"`
				AppliedCount int    `json:"appliedCount"`
			} `json:"middlewares"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			fail(fmt.Errorf("decoding response: %w", err))
		}

		if len(result.Middlewares) == 0 {
			fmt.Println("No middlewares defined.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tSCOPE\tAPPLIED\tSTATUS")
		for _, mw := range result.Middlewares {
			scope := mw.Project
			if scope == "" {
				scope = "all projects"
			}
			status := "ok"
			if !mw.Ready {
				// The reason is the useful part -- an unsupported ingress class reads very
				// differently from a spec that will not compile.
				status = "inactive"
				if mw.Reason != "" {
					status = "inactive: " + truncate(mw.Reason, 50)
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", mw.Name, mw.Type, scope, mw.AppliedCount, status)
		}
		w.Flush()
	},
}

var middlewaresGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show one middleware",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		raw, err := apiRequest("GET", "/api/v1/middlewares/"+args[0], nil)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var middlewaresCreateCmd = &cobra.Command{
	Use:   "create -f <file.json>",
	Short: "Create a middleware from a YAML or JSON file",
	Long: "Create a middleware from a YAML or JSON file, e.g.:\n\n" +
		"  name: office-only\n  type: ipAllowList\n" +
		"  description: Office and VPN ranges\n" +
		"  config:\n    sourceRange:\n      - 10.0.0.0/8\n\n" +
		"For basicAuth, give users directly and Vesta hashes them into a Secret it owns:\n\n" +
		"  name: staging-gate\n  type: basicAuth\n" +
		"  config:\n    users:\n      - username: alice\n        password: ...\n\n" +
		"Or name a Secret you manage yourself with secretName. Not both.\n\n" +
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

		raw, err := apiRequest("POST", "/api/v1/middlewares", payload)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var middlewaresDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a middleware",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := "/api/v1/middlewares/" + args[0]
		if force, _ := cmd.Flags().GetBool("force"); force {
			// Without force the server refuses while apps still reference it, because
			// Traefik drops an entire router whose middleware is missing -- deleting one
			// in use takes the site down rather than merely relaxing a policy.
			path += "?force=true"
		}
		raw, err := apiRequest("DELETE", path, nil)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var middlewaresAttachCmd = &cobra.Command{
	Use:   "attach <name> --app <app-id> --env <environment>",
	Short: "Attach a middleware to an app environment",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		app, env := mustAppEnv(cmd)

		current, err := currentMiddlewares(app, env)
		if err != nil {
			fail(err)
		}
		for _, name := range current {
			if name == args[0] {
				fmt.Printf("%s is already attached to %s/%s\n", args[0], app, env)
				return
			}
		}

		// Appends, because position is meaningful and the safe default is "after the
		// policies already in force" rather than ahead of them.
		updated := append(current, args[0])
		if err := setMiddlewares(app, env, updated); err != nil {
			fail(err)
		}
		fmt.Printf("Attached. %s/%s now applies: %s\n", app, env, strings.Join(updated, " -> "))
	},
}

var middlewaresDetachCmd = &cobra.Command{
	Use:   "detach <name> --app <app-id> --env <environment>",
	Short: "Detach a middleware from an app environment",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		app, env := mustAppEnv(cmd)

		current, err := currentMiddlewares(app, env)
		if err != nil {
			fail(err)
		}

		updated := make([]string, 0, len(current))
		found := false
		for _, name := range current {
			if name == args[0] {
				found = true
				continue
			}
			updated = append(updated, name)
		}
		if !found {
			fmt.Printf("%s is not attached to %s/%s\n", args[0], app, env)
			return
		}

		if err := setMiddlewares(app, env, updated); err != nil {
			fail(err)
		}
		if len(updated) == 0 {
			fmt.Printf("Detached. %s/%s now applies no middlewares.\n", app, env)
			return
		}
		fmt.Printf("Detached. %s/%s now applies: %s\n", app, env, strings.Join(updated, " -> "))
	},
}

func currentMiddlewares(app, env string) ([]string, error) {
	raw, err := apiRequest("GET", fmt.Sprintf("/api/v1/apps/%s/middlewares?environment=%s", app, env), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Middlewares []string `json:"middlewares"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.Middlewares, nil
}

func setMiddlewares(app, env string, names []string) error {
	_, err := apiRequest("PUT", fmt.Sprintf("/api/v1/apps/%s/middlewares", app), map[string]interface{}{
		"environment": env,
		"middlewares": names,
	})
	return err
}

func mustAppEnv(cmd *cobra.Command) (string, string) {
	app, _ := cmd.Flags().GetString("app")
	env, _ := cmd.Flags().GetString("env")
	if app == "" || env == "" {
		fail(fmt.Errorf("--app and --env are both required"))
	}
	return app, env
}

// readConfigFile accepts YAML or JSON. JSON is valid YAML, so one parser handles both --
// and the file people have to hand is usually a YAML manifest.
func readConfigFile(path string, out interface{}) error {
	var data []byte
	var err error
	if path == "-" {
		data, err = readAllStdin()
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s is not valid YAML or JSON: %w", path, err)
	}
	return nil
}

func readAllStdin() ([]byte, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Mode()&os.ModeCharDevice != 0 {
		return nil, fmt.Errorf("no input on stdin")
	}
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}

func printJSON(raw []byte) {
	var pretty interface{}
	if err := json.Unmarshal(raw, &pretty); err != nil {
		fmt.Println(string(raw))
		return
	}
	formatted, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(formatted))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func init() {
	middlewaresListCmd.Flags().String("project", "", "Only show middlewares available to this project")
	middlewaresCreateCmd.Flags().StringP("file", "f", "", "YAML or JSON file describing the middleware, or - for stdin")
	middlewaresDeleteCmd.Flags().Bool("force", false, "Delete even while apps still reference it")

	for _, c := range []*cobra.Command{middlewaresAttachCmd, middlewaresDetachCmd} {
		c.Flags().String("app", "", "App ID (required)")
		c.Flags().String("env", "", "Environment (required)")
	}

	middlewaresCmd.AddCommand(
		middlewaresListCmd, middlewaresGetCmd, middlewaresCreateCmd,
		middlewaresDeleteCmd, middlewaresAttachCmd, middlewaresDetachCmd,
	)
	rootCmd.AddCommand(middlewaresCmd)
}
