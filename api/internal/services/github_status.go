package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// GitHubStatusNotifier sends deployment statuses back to GitHub using the
// Deployments API: https://docs.github.com/en/rest/deployments
type GitHubStatusNotifier struct {
	client *http.Client
}

func NewGitHubStatusNotifier() *GitHubStatusNotifier {
	return &GitHubStatusNotifier{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// CreateDeployment creates a GitHub Deployment object and returns its ID.
func (g *GitHubStatusNotifier) CreateDeployment(ctx context.Context, token, repo, ref, environment, description string) (int64, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/deployments", repo)

	payload := map[string]interface{}{
		"ref":               ref,
		"environment":       environment,
		"description":       description,
		"auto_merge":        false,
		"required_contexts": []string{},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github create deployment: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		return 0, fmt.Errorf("github create deployment: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("github parse deployment response: %w", err)
	}

	return result.ID, nil
}

// UpdateDeploymentStatus updates the status of a GitHub Deployment.
// state: "pending", "in_progress", "success", "failure", "error", "inactive"
func (g *GitHubStatusNotifier) UpdateDeploymentStatus(ctx context.Context, token, repo string, deploymentID int64, state, environmentURL, description, logURL string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/deployments/%d/statuses", repo, deploymentID)

	payload := map[string]interface{}{
		"state":       state,
		"description": description,
	}
	if environmentURL != "" {
		payload["environment_url"] = environmentURL
	}
	if logURL != "" {
		payload["log_url"] = logURL
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("github update deployment status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github update deployment status: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// CreateCommitStatus sets a commit status (check) on a specific SHA.
// state: "pending", "success", "failure", "error"
func (g *GitHubStatusNotifier) CreateCommitStatus(ctx context.Context, token, repo, sha, state, targetURL, description, statusContext string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/statuses/%s", repo, sha)

	payload := map[string]interface{}{
		"state":       state,
		"description": description,
		"context":     statusContext,
	}
	if targetURL != "" {
		payload["target_url"] = targetURL
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("github create commit status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github create commit status: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// NotifyBuildStatus is a convenience method that reports build status to both
// the GitHub Deployments API and the commit status API.
func (g *GitHubStatusNotifier) NotifyBuildStatus(ctx context.Context, token, repo, commitSHA, environment, state, appURL, logURL, description string) {
	if token == "" || repo == "" {
		return
	}

	// Set commit status
	statusCtx := fmt.Sprintf("vesta/%s", environment)
	if err := g.CreateCommitStatus(ctx, token, repo, commitSHA, state, logURL, description, statusCtx); err != nil {
		log.Printf("[github-status] failed to set commit status on %s/%s: %v", repo, commitSHA[:8], err)
	}
}
