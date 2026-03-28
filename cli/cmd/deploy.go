package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [app-id]",
	Short: "Deploy a new image tag to an app",
	Long:  `Triggers a deployment by updating the image tag. The repository and imagePullSecrets are already configured on the app.`,
	Args:  cobra.ExactArgs(1),
	Run:   runDeploy,
}

var (
	deployTag       string
	deployReason    string
	deployCommitSHA string
)

func init() {
	deployCmd.Flags().StringVar(&deployTag, "tag", "", "Image tag to deploy (required)")
	deployCmd.Flags().StringVar(&deployReason, "reason", "", "Deploy reason")
	deployCmd.Flags().StringVar(&deployCommitSHA, "commit", "", "Git commit SHA")
	_ = deployCmd.MarkFlagRequired("tag")
	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) {
	appId := args[0]

	body := map[string]interface{}{
		"tag": deployTag,
	}
	if deployReason != "" {
		body["reason"] = deployReason
	}
	if deployCommitSHA != "" {
		body["commitSHA"] = deployCommitSHA
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/v1/apps/%s/deploy", apiURL, appId)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "Deploy failed (HTTP %d): %s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	fmt.Printf("Deployment triggered for %s with tag %s\n", appId, deployTag)

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err == nil {
		if id, ok := result["id"]; ok {
			fmt.Printf("Deploy ID: %v\n", id)
		}
		if status, ok := result["status"]; ok {
			fmt.Printf("Status: %v\n", status)
		}
	}
}
