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

func init() {
	secretsCmd.AddCommand(secretsListCmd)
	rootCmd.AddCommand(secretsCmd)
}
