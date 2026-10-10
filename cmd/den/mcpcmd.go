package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/lukaszgard/den/internal/mcpserver"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/service"
)

// mcpCmd implements `den mcp`.
func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve den to AI agents over the Model Context Protocol (stdio)",
		Long: "Run an MCP server on stdin/stdout so an AI agent (Claude Code, Cursor, …) can\n" +
			"list den's services, open a tunnel and read its status and logs — and, when\n" +
			"den.yaml's mcp.allow_runbooks is on, run runbooks.\n\n" +
			"Credentials are never returned to the agent. Production services are refused\n" +
			"unless mcp.allow_production is on, and then need your confirmation in the MCP\n" +
			"client for each connect. Tunnels the agent opens close when it disconnects.\n\n" +
			"Register it with Claude Code:\n" +
			"  claude mcp add den -- den mcp -c ~/.config/den/den.yaml",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, cfg, err := loadConfig(cfgFile)
			if err != nil {
				return err
			}
			if cfg == nil {
				return errNoConfig()
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// stdout carries the protocol: everything else goes to stderr.
			mgr := service.NewManager()
			logSkipped(applyConfig(ctx, mgr, cfg))
			reg, err := runbook.NewRegistry(cfg, path, livePorts(mgr))
			if err != nil {
				return err
			}
			// An agent's runs show up in the TUI's History tab too.
			reg.SetHistory(runbook.UserHistory(), func(err error) { fmt.Fprintf(os.Stderr, "warning: %v\n", err) })
			useUserRunbookDirs(reg)
			srv := mcpserver.New(mcpserver.Options{Manager: mgr, Config: cfg, Runbooks: reg, Version: version})
			return srv.Run(ctx, &mcp.StdioTransport{})
		},
	}
}

// livePorts exposes the local ports of the tunnels this process has open to
// runbooks, as DEN_PORT_<TYPE>_<NAME>.
func livePorts(mgr *service.Manager) runbook.PortSource {
	return runbook.PortsFrom(func() []runbook.ServiceState {
		var out []runbook.ServiceState
		for _, svc := range mgr.Services() {
			info, st := svc.Info(), svc.State()
			out = append(out, runbook.ServiceState{
				Type: info.Type, Name: info.Name, LocalPort: st.LocalPort,
				Connected: st.Status == service.StatusConnected,
			})
		}
		return out
	})
}
