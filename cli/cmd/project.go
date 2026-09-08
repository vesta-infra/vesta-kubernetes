package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects and move them between Vesta instances",
}

var projectExportCmd = &cobra.Command{
	Use:   "export <project-id>",
	Short: "Export a project as a bundle sealed for another Vesta instance",
	Long: `Export a project — its apps, configuration and secrets — as an encrypted bundle.

The bundle is sealed with the target instance's public key, which that instance shows
under Settings -> Instance Identity. Only that instance can open it, so the file is safe
to send over any channel that would not be safe for the secrets themselves.

The CLI never sees the plaintext: sealing happens on the exporting instance and opening
on the importing one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID := args[0]
		rawKey, _ := cmd.Flags().GetString("recipient-key")
		out, _ := cmd.Flags().GetString("out")

		key, err := readRecipientKey(rawKey)
		if err != nil {
			return err
		}

		body, err := apiRequest("POST", "/api/v1/projects/"+projectID+"/export",
			map[string]string{"recipientPublicKey": key})
		if err != nil {
			return err
		}

		if out == "" {
			out = fmt.Sprintf("vesta-%s.bundle.json", projectID)
		}
		// 0600: the bundle is unreadable without the target's key, but there is no reason
		// to leave it group- or world-readable on the way there.
		if err := os.WriteFile(out, body, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", out, err)
		}

		var envelope struct {
			Recipient string `json:"recipient"`
		}
		json.Unmarshal(body, &envelope)
		fmt.Printf("Exported %s to %s (sealed for %s)\n", projectID, out, envelope.Recipient)
		return nil
	},
}

var projectImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a project bundle sealed for this Vesta instance",
	Long: `Import a project bundle into the instance named by --api-url.

The bundle must have been sealed with this instance's public key. If a project of the same
name already exists it is never overwritten -- pass --as to import under a different name.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		importAs, _ := cmd.Flags().GetString("as")

		raw, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading %s: %w", file, err)
		}

		var envelope map[string]interface{}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("%s is not a Vesta bundle: %w", file, err)
		}

		payload := map[string]interface{}{"bundle": envelope}
		if importAs != "" {
			payload["as"] = importAs
		}

		body, err := apiRequest("POST", "/api/v1/projects/import", payload)
		if err != nil {
			return err
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)
		formatted, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(formatted))
		return nil
	},
}

// readRecipientKey accepts the key inline or, with a leading @, from a file — keys are
// long enough that pasting one into a shell is awkward and prone to truncation.
func readRecipientKey(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("--recipient-key is required (the target instance's public key, or @file)")
	}
	if !strings.HasPrefix(value, "@") {
		return strings.TrimSpace(value), nil
	}
	raw, err := os.ReadFile(strings.TrimPrefix(value, "@"))
	if err != nil {
		return "", fmt.Errorf("reading recipient key: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func init() {
	projectExportCmd.Flags().String("recipient-key", "", "Target instance's public key, or @file to read it from disk (required)")
	projectExportCmd.Flags().String("out", "", "Output file (default vesta-<project>.bundle.json)")
	projectExportCmd.MarkFlagRequired("recipient-key")

	projectImportCmd.Flags().String("file", "", "Bundle file to import (required)")
	projectImportCmd.Flags().String("as", "", "Import under a different project name")
	projectImportCmd.MarkFlagRequired("file")

	projectCmd.AddCommand(projectExportCmd)
	projectCmd.AddCommand(projectImportCmd)
	rootCmd.AddCommand(projectCmd)
}
