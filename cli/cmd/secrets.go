package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secrets (values are write-only)",
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secrets (metadata only)",
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("%s/api/v1/secrets", apiURL)
		req, _ := http.NewRequest("GET", url, nil)
		if apiToken != "" {
			req.Header.Set("Authorization", "Bearer "+apiToken)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		json.Unmarshal(body, &result)

		formatted, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(formatted))
	},
}

var secretsUnbindCmd = &cobra.Command{
	Use:   "unbind <secret-name> --app <app-id> [--env <environment>]",
	Short: "Unbind a shared secret from an app",
	Long:  "Unbind a shared secret from an app. If --env is given, unbinds only that environment; otherwise unbinds from all environments.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		secretName := args[0]
		appID, _ := cmd.Flags().GetString("app")
		env, _ := cmd.Flags().GetString("env")

		url := fmt.Sprintf("%s/api/v1/apps/%s/shared-secrets/%s", apiURL, appID, secretName)
		if env != "" {
			url += "?environment=" + env
		}

		req, _ := http.NewRequest("DELETE", url, nil)
		if apiToken != "" {
			req.Header.Set("Authorization", "Bearer "+apiToken)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNoContent {
			if env != "" {
				fmt.Printf("Unbound shared secret %q from app %q environment %q\n", secretName, appID, env)
			} else {
				fmt.Printf("Unbound shared secret %q from app %q (all environments)\n", secretName, appID)
			}
		} else {
			body, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "Error (%d): %s\n", resp.StatusCode, string(body))
			os.Exit(1)
		}
	},
}

func init() {
	secretsUnbindCmd.Flags().String("app", "", "App ID (required)")
	secretsUnbindCmd.Flags().String("env", "", "Environment to unbind from (optional, omit for all)")
	secretsUnbindCmd.MarkFlagRequired("app")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsUnbindCmd)
	rootCmd.AddCommand(secretsCmd)
}
