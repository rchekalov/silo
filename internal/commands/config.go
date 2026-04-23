// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/rchekalov/silo/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage project configuration (.siloconf)",
}

var configPortsCmd = &cobra.Command{
	Use:   "ports",
	Short: "Manage TCP port forwarding per tool",
}

var configPortsAddCmd = &cobra.Command{
	Use:     "add <tool> <host:guest>",
	Aliases: []string{"add-port"},
	Short:   "Add a TCP port forwarding rule",
	Args:    cobra.ExactArgs(2),
	RunE:    runConfigPortsAdd,
}

var configPortsRemoveCmd = &cobra.Command{
	Use:     "remove <tool> <host:guest>",
	Aliases: []string{"remove-port"},
	Short:   "Remove a TCP port forwarding rule",
	Args:    cobra.ExactArgs(2),
	RunE:    runConfigPortsRemove,
}

// Legacy flat forms. Kept hidden; print a deprecation note on use.
var configAddPortCmd = &cobra.Command{
	Use:    "add-port <tool> <host:guest>",
	Short:  "Deprecated: use `silo config ports add`",
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE:   runConfigPortsAdd,
}

var configRemovePortCmd = &cobra.Command{
	Use:    "remove-port <tool> <host:guest>",
	Short:  "Deprecated: use `silo config ports remove`",
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE:   runConfigPortsRemove,
}

func runConfigPortsAdd(cmd *cobra.Command, args []string) error {
	warnPortsDeprecation(cmd)
	host, guest, err := parsePortMapping(args[1])
	if err != nil {
		return err
	}
	cfg, root, err := loadOrInitProjectConfig()
	if err != nil {
		return err
	}
	cfg.AddPort(args[0], host, guest)
	return cfg.Save(root)
}

func runConfigPortsRemove(cmd *cobra.Command, args []string) error {
	warnPortsDeprecation(cmd)
	host, guest, err := parsePortMapping(args[1])
	if err != nil {
		return err
	}
	cfg, root, err := loadOrInitProjectConfig()
	if err != nil {
		return err
	}
	if !cfg.RemovePort(args[0], host, guest) {
		return fmt.Errorf("no such port mapping %d:%d on %q", host, guest, args[0])
	}
	return cfg.Save(root)
}

func warnPortsDeprecation(cmd *cobra.Command) {
	if called := cmd.CalledAs(); called == "add-port" || called == "remove-port" {
		nu := "ports add"
		if called == "remove-port" {
			nu = "ports remove"
		}
		fmt.Fprintf(os.Stderr, "note: `silo config %s` is now `silo config %s`; the alias will be removed in 0.6.0.\n", called, nu)
	}
}

var configNetworkCmd = &cobra.Command{
	Use:   "network",
	Short: "Manage network allow/deny rules per tool",
}

var configNetworkAllowCmd = &cobra.Command{
	Use:   "allow <tool> <domain>",
	Short: "Add a domain to the proxy allowlist",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadOrInitProjectConfig()
		if err != nil {
			return err
		}
		cfg.AddNetworkAllow(args[0], args[1])
		return cfg.Save(root)
	},
}

var configNetworkDenyCmd = &cobra.Command{
	Use:   "deny <tool> <domain>",
	Short: "Add a domain to the proxy denylist",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadOrInitProjectConfig()
		if err != nil {
			return err
		}
		cfg.AddNetworkDeny(args[0], args[1])
		return cfg.Save(root)
	},
}

var configNetworkRemoveCmd = &cobra.Command{
	Use:   "remove <tool> <domain>",
	Short: "Remove a domain from allow and deny lists",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadOrInitProjectConfig()
		if err != nil {
			return err
		}
		if !cfg.RemoveNetworkDomain(args[0], args[1]) {
			return fmt.Errorf("domain %q not found in %q's rules", args[1], args[0])
		}
		return cfg.Save(root)
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the merged project config as YAML",
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, err := config.ResolveWorkspace("")
		if err != nil {
			return err
		}
		if ws.Merged == nil {
			fmt.Fprintln(os.Stderr, "No project or global .siloconf found.")
			return nil
		}
		merged := ws.Merged
		out, err := yaml.Marshal(merged)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
		return nil
	},
}

func parsePortMapping(s string) (host, guest uint16, err error) {
	h, g, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("expected host:guest, got %q", s)
	}
	hi, err := strconv.Atoi(h)
	if err != nil || hi < 1 || hi > 65535 {
		return 0, 0, fmt.Errorf("invalid host port %q", h)
	}
	gi, err := strconv.Atoi(g)
	if err != nil || gi < 1 || gi > 65535 {
		return 0, 0, fmt.Errorf("invalid guest port %q", g)
	}
	return uint16(hi), uint16(gi), nil
}

func loadOrInitProjectConfig() (*config.ProjectConfig, string, error) {
	cfg, root, err := config.FindOrDefault()
	if err != nil {
		return nil, "", err
	}
	return cfg, root, nil
}

func init() {
	configNetworkCmd.AddCommand(configNetworkAllowCmd, configNetworkDenyCmd, configNetworkRemoveCmd)
	configPortsCmd.AddCommand(configPortsAddCmd, configPortsRemoveCmd)
	configCmd.AddCommand(configPortsCmd, configNetworkCmd, configShowCmd, configAddPortCmd, configRemovePortCmd)
	addCommand(configCmd)
}
