package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var appsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Manage apps",
}

var appsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all apps",
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("%s/api/v1/apps", apiURL)
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

var appsGetCmd = &cobra.Command{
	Use:   "get [app-id]",
	Short: "Get app details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("%s/api/v1/apps/%s", apiURL, args[0])
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
	appsCmd.AddCommand(appsListCmd)
	appsCmd.AddCommand(appsGetCmd)
	rootCmd.AddCommand(appsCmd)
}
