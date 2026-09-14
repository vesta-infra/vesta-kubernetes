package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// Managed add-ons: Postgres, MySQL, Redis and MongoDB run by Vesta itself.

var addonsCmd = &cobra.Command{
	Use:     "addons",
	Aliases: []string{"addon"},
	Short:   "Manage managed datastores",
	Long: `Managed add-ons are datastores Vesta runs for a project.

Each is a single-replica StatefulSet with its own volume and generated credentials. Binding
one to an app injects its connection details as environment variables, including
DATABASE_URL.`,
}

var addonsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List a project's add-ons",
	Run: func(cmd *cobra.Command, args []string) {
		project, _ := cmd.Flags().GetString("project")
		if project == "" {
			fail(fmt.Errorf("--project is required"))
		}

		raw, err := apiRequest("GET", "/api/v1/projects/"+project+"/addons", nil)
		if err != nil {
			fail(err)
		}

		var out struct {
			Items []struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				Version     string `json:"version"`
				Environment string `json:"environment"`
				Ready       bool   `json:"ready"`
				Reason      string `json:"reason"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			fail(err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tVERSION\tENVIRONMENT\tREADY\tDETAIL")
		for _, a := range out.Items {
			env := a.Environment
			if env == "" {
				env = "(all)"
			}
			ready := "no"
			if a.Ready {
				ready = "yes"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				a.Name, a.Type, orDash(a.Version), env, ready, truncate(a.Reason, 40))
		}
		w.Flush()
	},
}

var addonsCreateCmd = &cobra.Command{
	Use:   "create -f <file>",
	Short: "Create an add-on from a YAML or JSON file",
	Long: `Create a managed datastore.

  name: cache
  type: redis          # postgres | mysql | redis | mongodb
  version: "7"         # optional; a sensible major is pinned by default
  environment: production   # optional; omit for one instance per environment
  size: small          # optional pod size preset
  storage: 10Gi
  deletionPolicy: Retain    # Retain (default) keeps the volume when the add-on is deleted

Use "-" to read from stdin.`,
	Run: func(cmd *cobra.Command, args []string) {
		project, _ := cmd.Flags().GetString("project")
		if project == "" {
			fail(fmt.Errorf("--project is required"))
		}
		file, _ := cmd.Flags().GetString("file")
		if file == "" {
			fail(fmt.Errorf("--file is required"))
		}

		var payload map[string]interface{}
		if err := readConfigFile(file, &payload); err != nil {
			fail(err)
		}

		raw, err := apiRequest("POST", "/api/v1/projects/"+project+"/addons", payload)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var addonsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an add-on",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		project, _ := cmd.Flags().GetString("project")
		if project == "" {
			fail(fmt.Errorf("--project is required"))
		}
		force, _ := cmd.Flags().GetBool("force")
		if !force {
			// The server refuses without this too. Saying so here saves a round trip and
			// makes the consequence explicit before anything is sent.
			fail(fmt.Errorf("deleting an add-on removes a running datastore; repeat with --force"))
		}

		raw, err := apiRequest("DELETE",
			"/api/v1/projects/"+project+"/addons/"+args[0]+"?force=true", nil)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var addonsCredentialsCmd = &cobra.Command{
	Use:   "credentials <name>",
	Short: "Show an add-on's connection details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		project, _ := cmd.Flags().GetString("project")
		env, _ := cmd.Flags().GetString("env")
		if project == "" || env == "" {
			fail(fmt.Errorf("--project and --env are both required: credentials differ per environment"))
		}

		raw, err := apiRequest("GET",
			"/api/v1/projects/"+project+"/addons/"+args[0]+"/credentials?environment="+env, nil)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var addonsBindCmd = &cobra.Command{
	Use:   "bind <name>",
	Short: "Inject an add-on's credentials into an app",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		if app == "" {
			fail(fmt.Errorf("--app is required"))
		}
		envs, _ := cmd.Flags().GetStringSlice("env")

		payload := map[string]interface{}{"addon": args[0]}
		if len(envs) > 0 {
			payload["environments"] = envs
		}

		raw, err := apiRequest("POST", "/api/v1/apps/"+app+"/addons", payload)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

var addonsUnbindCmd = &cobra.Command{
	Use:   "unbind <name>",
	Short: "Stop injecting an add-on's credentials into an app",
	Long:  "Removes the environment variables. The add-on and its data are untouched.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		app, _ := cmd.Flags().GetString("app")
		if app == "" {
			fail(fmt.Errorf("--app is required"))
		}

		raw, err := apiRequest("DELETE", "/api/v1/apps/"+app+"/addons/"+args[0], nil)
		if err != nil {
			fail(err)
		}
		printJSON(raw)
	},
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func init() {
	for _, c := range []*cobra.Command{
		addonsListCmd, addonsCreateCmd, addonsDeleteCmd, addonsCredentialsCmd,
	} {
		c.Flags().String("project", "", "Project the add-on belongs to")
	}
	addonsCreateCmd.Flags().StringP("file", "f", "", "YAML or JSON file describing the add-on, or - for stdin")
	addonsDeleteCmd.Flags().Bool("force", false, "Confirm that a running datastore is being removed")
	addonsCredentialsCmd.Flags().String("env", "", "Environment whose credentials to show")

	addonsBindCmd.Flags().String("app", "", "App to inject the credentials into")
	addonsBindCmd.Flags().StringSlice("env", nil, "Limit the binding to these environments")
	addonsUnbindCmd.Flags().String("app", "", "App to remove the credentials from")

	addonsCmd.AddCommand(addonsListCmd, addonsCreateCmd, addonsDeleteCmd,
		addonsCredentialsCmd, addonsBindCmd, addonsUnbindCmd)
	rootCmd.AddCommand(addonsCmd)
}
