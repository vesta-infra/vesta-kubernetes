package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var pipelinesCmd = &cobra.Command{
	Use:   "pipelines",
	Short: "Manage pipelines",
}

var pipelinesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all pipelines",
	Run: func(cmd *cobra.Command, args []string) {
		url := fmt.Sprintf("%s/api/v1/pipelines", apiURL)
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
	pipelinesCmd.AddCommand(pipelinesListCmd)
	rootCmd.AddCommand(pipelinesCmd)
}
