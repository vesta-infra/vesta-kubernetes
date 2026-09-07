package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// chartRef is fixed. The version is the only thing a caller chooses; accepting an
// arbitrary chart would turn `vesta install` into "run any helm chart against my cluster".
const chartRef = "oci://ghcr.io/vesta-infra/charts/vesta"

// requireTool fails with an actionable message rather than exec's "file not found".
func requireTool(name, hint string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is required but not installed. %s", name, hint)
	}
	return nil
}

func requireHelm() error {
	return requireTool("helm", "Install it from https://helm.sh/docs/intro/install/")
}

// runHelm streams helm's own output through, so a failing install shows helm's diagnosis
// rather than this command's summary of it.
func runHelm(args []string, dryRun bool) error {
	if dryRun {
		fmt.Printf("helm %s\n", strings.Join(args, " "))
		return nil
	}
	cmd := exec.Command("helm", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// helmArgs assembles the flags shared by install and upgrade.
func helmArgs(action, namespace, release, version, database, ingressClass string, postgres bool) []string {
	args := []string{action}
	if action == "upgrade" {
		// --install makes `vesta upgrade` work on a cluster that has never had Vesta,
		// which is what someone re-running it in CI expects.
		args = append(args, "--install")
	}
	args = append(args, release, chartRef, "--namespace", namespace)

	if action == "install" || action == "upgrade" {
		args = append(args, "--create-namespace")
	}
	if version != "" {
		args = append(args, "--version", version)
	}
	if action == "upgrade" {
		// Not --reuse-values: that carries old values forward wholesale and silently
		// drops new chart defaults, so an upgrade ends up running new images against the
		// previous release's configuration.
		args = append(args, "--reset-then-reuse-values")
	}
	// Configuration is only ever applied at install time. On upgrade
	// --reset-then-reuse-values carries the installed configuration forward, and
	// re-specifying any of it here is how an upgrade quietly changes a setting nobody
	// meant to change. Enforced here rather than left to callers, so a future flag on
	// the upgrade command cannot reintroduce it by accident.
	if action == "install" {
		if database != "" {
			// --set-string, not --set: a connection string can contain commas and braces
			// that helm's --set parses as structure.
			args = append(args, "--set-string", "api.database.url="+database)
		} else if postgres {
			args = append(args, "--set", "postgres.enabled=true")
		}
		if ingressClass != "" {
			args = append(args, "--set-string", "config.ingressClassName="+ingressClass)
		}
	}
	return append(args, "--wait", "--timeout", "10m")
}

var platformInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install Vesta into a Kubernetes cluster",
	Long: `Install the Vesta platform with Helm.

Wraps the same helm command the documentation gives, so there is nothing this does that
you could not do by hand -- it just removes the flags you would otherwise have to
remember. CRDs are applied by the chart.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireHelm(); err != nil {
			return err
		}
		ns, _ := cmd.Flags().GetString("namespace")
		release, _ := cmd.Flags().GetString("release")
		ver, _ := cmd.Flags().GetString("chart-version")
		db, _ := cmd.Flags().GetString("database-url")
		ing, _ := cmd.Flags().GetString("ingress-class")
		pg, _ := cmd.Flags().GetBool("postgres")
		dry, _ := cmd.Flags().GetBool("dry-run")

		if db == "" && !pg {
			return fmt.Errorf("give a database with --database-url, or --postgres to deploy a bundled one")
		}

		if err := runHelm(helmArgs("install", ns, release, ver, db, ing, pg), dry); err != nil {
			return err
		}
		if !dry {
			fmt.Printf("\nVesta installed in %s.\n", ns)
			fmt.Printf("  kubectl port-forward -n %s svc/%s-ui 8080:80\n", ns, release)
			fmt.Println("  then open http://localhost:8080/setup")
		}
		return nil
	},
}

var platformUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the Vesta platform",
	Long: `Upgrade Vesta with Helm.

CRDs are applied by a pre-upgrade hook in the chart, so the manual kubectl apply that
earlier versions needed is not required.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireHelm(); err != nil {
			return err
		}
		ns, _ := cmd.Flags().GetString("namespace")
		release, _ := cmd.Flags().GetString("release")
		ver, _ := cmd.Flags().GetString("chart-version")
		dry, _ := cmd.Flags().GetBool("dry-run")

		if ver == "" {
			fmt.Println("Checking for the latest release...")
			latest, err := latestRelease()
			if err != nil {
				// An air-gapped cluster cannot reach GitHub, and helm will still resolve
				// the newest chart from the registry, so this is not fatal.
				fmt.Fprintf(os.Stderr, "warning: %v; upgrading to the newest chart the registry offers\n", err)
			} else {
				ver = latest
				fmt.Printf("Upgrading to %s...\n", ver)
			}
		}

		// Database and ingress flags are deliberately absent: --reset-then-reuse-values
		// carries the installed configuration forward, and re-specifying it here is how
		// an upgrade quietly changes settings nobody meant to change.
		return runHelm(helmArgs("upgrade", ns, release, ver, "", "", false), dry)
	},
}

var platformStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the running Vesta version and whether an update is available",
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := apiRequest("GET", "/api/v1/system/version", nil)
		if err != nil {
			return err
		}
		var sys struct {
			API struct {
				Version string `json:"version"`
			} `json:"api"`
			Components []struct {
				Component string `json:"component"`
				Tag       string `json:"tag"`
				Ready     bool   `json:"ready"`
			} `json:"components"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal(body, &sys); err != nil {
			return fmt.Errorf("decoding version: %w", err)
		}

		fmt.Printf("Vesta in namespace %s\n\n", sys.Namespace)
		for _, c := range sys.Components {
			state := "not ready"
			if c.Ready {
				state = "ready"
			}
			fmt.Printf("  %-10s %-10s %s\n", c.Component, c.Tag, state)
		}

		// Reported separately from the components: this is the server's cached view of
		// the release feed, and an instance with checks disabled has nothing to say.
		if body, err := apiRequest("GET", "/api/v1/system/update", nil); err == nil {
			var upd struct {
				Latest          string `json:"latest"`
				UpdateAvailable bool   `json:"updateAvailable"`
				CheckEnabled    bool   `json:"checkEnabled"`
			}
			if json.Unmarshal(body, &upd) == nil {
				fmt.Println()
				switch {
				case !upd.CheckEnabled:
					fmt.Println("  update checks are disabled on this instance")
				case upd.UpdateAvailable:
					fmt.Printf("  update available: %s  (vesta upgrade)\n", upd.Latest)
				default:
					fmt.Println("  up to date")
				}
			}
		}

		fmt.Printf("\n  cli        %s\n", version)
		if !isDevBuild() {
			if latest, err := latestRelease(); err == nil && isNewer(version, latest) {
				fmt.Printf("  a newer CLI is available: %s  (vesta self-update)\n", latest)
			}
		}
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{platformInstallCmd, platformUpgradeCmd} {
		c.Flags().String("namespace", "vesta-system", "Namespace to install into")
		c.Flags().String("release", "vesta", "Helm release name")
		c.Flags().String("chart-version", "", "Chart version (default: latest)")
		c.Flags().Bool("dry-run", false, "Print the helm command without running it")
	}
	platformInstallCmd.Flags().String("database-url", "", "PostgreSQL connection string")
	platformInstallCmd.Flags().String("ingress-class", "", "Ingress class for deployed apps")
	platformInstallCmd.Flags().Bool("postgres", false, "Deploy a bundled PostgreSQL")

	rootCmd.AddCommand(platformInstallCmd)
	rootCmd.AddCommand(platformUpgradeCmd)
	rootCmd.AddCommand(platformStatusCmd)
}
