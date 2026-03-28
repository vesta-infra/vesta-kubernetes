package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	apiURL   string
	apiToken string
)

var rootCmd = &cobra.Command{
	Use:   "vesta",
	Short: "Vesta CLI -- manage Kubernetes app deployments",
	Long:  `Vesta is a Heroku-like PaaS for Kubernetes. This CLI manages pipelines, apps, secrets, and deployments.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "http://localhost:8090", "Vesta API server URL")
	rootCmd.PersistentFlags().StringVar(&apiToken, "token", "", "API token for authentication")
}
