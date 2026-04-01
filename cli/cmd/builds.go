package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var buildsCmd = &cobra.Command{
	Use:   "builds [app-id]",
	Short: "List builds for an app",
	Long:  `Shows the build history for an application including status, strategy, and commit info.`,
	Args:  cobra.ExactArgs(1),
	Run:   runListBuilds,
}

var buildCmd = &cobra.Command{
	Use:   "build [app-id]",
	Short: "Trigger a new build for an app",
	Long:  `Triggers a build using the app's configured build strategy (dockerfile, nixpacks, buildpacks).`,
	Args:  cobra.ExactArgs(1),
	Run:   runBuild,
}

var buildLogsCmd = &cobra.Command{
	Use:   "build-logs [app-id] [build-id]",
	Short: "Stream build logs",
	Long:  `Stream real-time build logs for a specific build. Use --follow for live streaming.`,
	Args:  cobra.ExactArgs(2),
	Run:   runBuildLogs,
}

var (
	buildCommit string
	buildBranch string
	buildEnv    string
	buildFollow bool
)

func init() {
	buildCmd.Flags().StringVar(&buildCommit, "commit", "", "Git commit SHA to build")
	buildCmd.Flags().StringVar(&buildBranch, "branch", "", "Git branch to build")
	buildCmd.Flags().StringVar(&buildEnv, "environment", "", "Target environment (required)")
	_ = buildCmd.MarkFlagRequired("environment")

	buildLogsCmd.Flags().BoolVarP(&buildFollow, "follow", "f", false, "Follow log output (stream)")

	rootCmd.AddCommand(buildsCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(buildLogsCmd)
}

func runListBuilds(cmd *cobra.Command, args []string) {
	appId := args[0]

	url := fmt.Sprintf("%s/api/v1/apps/%s/builds?limit=20", apiURL, appId)
	req, err := http.NewRequest("GET", url, nil)
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

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "Failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var result struct {
		Items []struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			Strategy    string `json:"strategy"`
			CommitSHA   string `json:"commitSha"`
			Branch      string `json:"branch"`
			Image       string `json:"image"`
			TriggeredBy string `json:"triggeredBy"`
			DurationMs  int    `json:"durationMs"`
			CreatedAt   string `json:"createdAt"`
			Error       string `json:"error,omitempty"`
		} `json:"items"`
		Total int `json:"total"`
	}
	json.Unmarshal(body, &result)

	if len(result.Items) == 0 {
		fmt.Println("No builds found.")
		return
	}

	fmt.Printf("%-36s  %-10s  %-12s  %-10s  %-8s  %s\n", "BUILD ID", "STATUS", "STRATEGY", "BRANCH", "SHA", "CREATED")
	fmt.Println("------------------------------------------------------------------------------------------------------------")
	for _, b := range result.Items {
		sha := b.CommitSHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		statusIcon := "⏳"
		switch b.Status {
		case "success":
			statusIcon = "✅"
		case "failed":
			statusIcon = "❌"
		case "running":
			statusIcon = "🔨"
		case "cancelled":
			statusIcon = "🚫"
		}
		fmt.Printf("%-36s  %s %-8s  %-12s  %-10s  %-8s  %s\n",
			b.ID, statusIcon, b.Status, b.Strategy, b.Branch, sha, b.CreatedAt)
	}
}

func runBuild(cmd *cobra.Command, args []string) {
	appId := args[0]

	payload := map[string]interface{}{
		"environment": buildEnv,
	}
	if buildCommit != "" {
		payload["commitSha"] = buildCommit
	}
	if buildBranch != "" {
		payload["branch"] = buildBranch
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/v1/apps/%s/builds", apiURL, appId)
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

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "Build failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var result struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Strategy string `json:"strategy"`
		Image    string `json:"image"`
		LogsURL  string `json:"logsUrl"`
	}
	json.Unmarshal(body, &result)

	fmt.Printf("Build triggered for %s\n", appId)
	fmt.Printf("  Build ID: %s\n", result.ID)
	fmt.Printf("  Strategy: %s\n", result.Strategy)
	fmt.Printf("  Image:    %s\n", result.Image)
	fmt.Printf("  Status:   %s\n", result.Status)
	fmt.Printf("\nStream logs with:\n  vesta build-logs %s %s --follow\n", appId, result.ID)
}

func runBuildLogs(cmd *cobra.Command, args []string) {
	appId := args[0]
	buildId := args[1]

	url := fmt.Sprintf("%s/api/v1/apps/%s/builds/%s/logs", apiURL, appId, buildId)
	if buildFollow {
		url += "?follow=true"
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	if buildFollow {
		// SSE stream
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > 6 && line[:6] == "data: " {
				fmt.Println(line[6:])
			} else if len(line) > 7 && line[:7] == "event: " {
				event := line[7:]
				if event == "done" {
					fmt.Println("\n--- Build finished ---")
				}
			}
		}
	} else {
		body, _ := io.ReadAll(resp.Body)
		var result struct {
			Status string `json:"status"`
			Logs   string `json:"logs"`
		}
		json.Unmarshal(body, &result)

		fmt.Printf("Build Status: %s\n\n", result.Status)
		if result.Logs != "" {
			fmt.Println(result.Logs)
		} else {
			fmt.Println("(no logs available)")
		}
	}
}
