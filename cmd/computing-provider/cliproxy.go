package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/swanchain/computing-provider-v2/internal/cliproxy"
	"github.com/urfave/cli/v2"
)

var cliproxyCmd = &cli.Command{
	Name:  "cliproxy",
	Usage: "Inspect and authenticate a local CLIProxyAPI backend",
	Description: `CLIProxyAPI turns a personal ChatGPT, Claude or Gemini subscription into an
OpenAI-compatible endpoint this provider can serve from.

It is a separate program, so these commands drive it rather than replacing it:
'status' reports what it holds and whether those models actually serve, and
'login' runs its OAuth flow.`,
	Subcommands: []*cli.Command{
		cliproxyStatusCmd,
		cliproxyLoginCmd,
	},
}

// loginProviders are CLIProxyAPI's OAuth flags, minus their -login suffix.
var loginProviders = []string{"codex", "claude", "kimi", "xai", "antigravity"}

var cliproxyStatusCmd = &cli.Command{
	Name:  "status",
	Usage: "Show stored CLIProxyAPI credentials, and optionally prove the models still serve",
	Description: `Reads the credential files and reports each one's provider, account, plan and
expiry. No token is read or printed.

Credentials alone cannot tell you whether traffic works: a credential can be
unexpired and enabled and still be rejected upstream, while the proxy keeps
answering /v1/models from a static list. Pass --probe to send one real
single-token completion per model and see what actually comes back.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "auth-dir",
			Usage: "Directory holding CLIProxyAPI credentials",
			Value: cliproxy.DefaultAuthDir,
		},
		&cli.StringFlag{
			Name:  "config",
			Usage: "CLIProxyAPI config.yaml to read auth-dir from (overrides --auth-dir)",
		},
		&cli.BoolFlag{
			Name:  "probe",
			Usage: "Send one single-token completion per model through the proxy",
		},
		&cli.StringFlag{
			Name:  "endpoint",
			Usage: "Proxy base URL to probe",
			Value: "http://127.0.0.1:8317",
		},
		&cli.StringFlag{
			Name:  "api-key",
			Usage: "API key the proxy expects (defaults to the api_key models.json already uses for it)",
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Print the report as JSON",
		},
	},
	Action: func(cctx *cli.Context) error {
		authDir := cctx.String("auth-dir")
		if configPath := cctx.String("config"); configPath != "" {
			fromConfig, err := cliproxy.AuthDirFromConfig(configPath)
			if err != nil {
				return fmt.Errorf("read auth-dir from %s: %w", configPath, err)
			}
			authDir = fromConfig
		}

		creds, err := cliproxy.ReadCredentials(authDir)
		if err != nil {
			return fmt.Errorf("read credentials from %s: %w", cliproxy.ExpandPath(authDir), err)
		}

		var probes []cliproxy.ProbeResult
		if cctx.Bool("probe") {
			models, apiKey, err := probeModels(cctx)
			if err != nil {
				return err
			}
			if len(models) == 0 {
				return fmt.Errorf("no models in models.json point at %s — pass --endpoint to match one, or name the models as arguments", cctx.String("endpoint"))
			}
			if flagKey := cctx.String("api-key"); flagKey != "" {
				apiKey = flagKey
			}
			ctx, cancel := context.WithTimeout(cctx.Context, 5*time.Minute)
			defer cancel()
			probes = cliproxy.Probe(ctx, cctx.String("endpoint"), apiKey, models)
		}

		if cctx.Bool("json") {
			out, err := json.MarshalIndent(map[string]any{
				"auth_dir":    cliproxy.ExpandPath(authDir),
				"credentials": creds,
				"probes":      probes,
			}, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(out))
		} else {
			printCliproxyStatus(cliproxy.ExpandPath(authDir), creds, probes, cctx.Bool("probe"))
		}

		// Non-zero when something is actually wrong, so this works as a
		// monitoring check the same way selfcheck does.
		for _, p := range probes {
			if !p.OK {
				return cli.Exit("", 1)
			}
		}
		for _, c := range creds {
			if c.State == cliproxy.StateExpired || c.State == cliproxy.StateDisabled || c.Err != "" {
				return cli.Exit("", 1)
			}
		}
		return nil
	},
}

// probeModels works out which model names to probe, and the key to reach them
// with: the ones models.json points at this endpoint, unless the operator named
// them explicitly as arguments.
func probeModels(cctx *cli.Context) (models []string, apiKey string, err error) {
	if args := cctx.Args().Slice(); len(args) > 0 {
		return args, "", nil
	}
	repoPath, ok := os.LookupEnv("CP_PATH")
	if !ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", fmt.Errorf("missing CP_PATH and no home directory: %w", err)
		}
		repoPath = filepath.Join(home, ".swan", "computing")
	}
	modelsPath := filepath.Join(repoPath, "models.json")
	models, apiKey, err = cliproxy.ModelsForEndpoint(modelsPath, cctx.String("endpoint"))
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", modelsPath, err)
	}
	return models, apiKey, nil
}

func printCliproxyStatus(authDir string, creds []cliproxy.Credential, probes []cliproxy.ProbeResult, probed bool) {
	bold := color.New(color.Bold).SprintFunc()
	fmt.Printf("\n%s  %s\n\n", bold("Credentials"), color.HiBlackString(authDir))

	if len(creds) == 0 {
		fmt.Printf("  %s no credentials found — run 'computing-provider cliproxy login'\n", color.YellowString("none"))
	}
	for _, c := range creds {
		var state string
		switch c.State {
		case cliproxy.StateValid:
			state = color.GreenString("%-9s", "valid")
		case cliproxy.StateExpiring:
			state = color.YellowString("%-9s", "expiring")
		case cliproxy.StateExpired:
			state = color.RedString("%-9s", "expired")
		case cliproxy.StateDisabled:
			state = color.RedString("%-9s", "disabled")
		default:
			state = color.HiBlackString("%-9s", "unknown")
		}

		account := c.Email
		if account == "" {
			account = c.File
		}
		if c.Plan != "" {
			account = fmt.Sprintf("%s (%s)", account, c.Plan)
		}

		fmt.Printf("  %s %-12s %s\n", state, c.Provider, account)
		if c.Err != "" {
			fmt.Printf("            %s\n", color.RedString(c.Err))
			continue
		}
		if !c.Expires.IsZero() {
			rel := time.Until(c.Expires).Round(time.Hour)
			when := fmt.Sprintf("expires %s", c.Expires.Format("2006-01-02 15:04 MST"))
			if rel < 0 {
				when += fmt.Sprintf(" (%s ago)", (-rel).String())
			} else {
				when += fmt.Sprintf(" (in %s)", rel.String())
			}
			fmt.Printf("            %s\n", color.HiBlackString(when))
		}
	}

	if !probed {
		fmt.Printf("\n  %s\n", color.HiBlackString("Add --probe to check the models actually serve; credentials alone cannot tell you."))
		fmt.Println()
		return
	}

	fmt.Printf("\n%s\n\n", bold("Live probe"))
	failed := 0
	for _, p := range probes {
		if p.OK {
			fmt.Printf("  %s %s\n", color.GreenString("%-9s", "ok"), p.Model)
			continue
		}
		failed++
		label := "fail"
		if p.Status > 0 {
			label = fmt.Sprintf("HTTP %d", p.Status)
		}
		fmt.Printf("  %s %s\n", color.RedString("%-9s", label), p.Model)
		if p.Detail != "" {
			fmt.Printf("            %s\n", color.HiBlackString(p.Detail))
		}
	}

	if failed > 0 {
		fmt.Printf("\n  %s\n", color.YellowString("%d of %d models did not serve.", failed, len(probes)))
		fmt.Printf("  %s\n", color.HiBlackString("A valid credential that still fails is an upstream rejection, not an expiry —"))
		fmt.Printf("  %s\n", color.HiBlackString("check the account's subscription, and CLIProxyAPI's own log for the status it got."))
	}
	fmt.Println()
}

var cliproxyLoginCmd = &cli.Command{
	Name:  "login",
	Usage: "Run CLIProxyAPI's OAuth login for a provider",
	Description: `Runs the CLIProxyAPI binary's own login flow and leaves the credential where the
proxy watches for it, so no restart is needed.

On a headless box use --device: it prints a code and a URL to open elsewhere,
instead of expecting a browser on this machine.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "provider",
			Usage: "One of: " + strings.Join(loginProviders, ", "),
			Value: "codex",
		},
		&cli.BoolFlag{
			Name:  "device",
			Usage: "Use the device-code flow (for machines with no browser)",
		},
		&cli.StringFlag{
			Name:  "bin",
			Usage: "Path to the CLIProxyAPI binary (searched on PATH by default)",
		},
		&cli.StringFlag{
			Name:  "config",
			Usage: "CLIProxyAPI config.yaml to use",
		},
		&cli.StringFlag{
			Name:  "auth-dir",
			Usage: "Directory the credential should land in (used to confirm the login actually wrote one)",
			Value: cliproxy.DefaultAuthDir,
		},
	},
	Action: func(cctx *cli.Context) error {
		provider := cctx.String("provider")
		valid := false
		for _, p := range loginProviders {
			if p == provider {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("unknown provider %q, expected one of: %s", provider, strings.Join(loginProviders, ", "))
		}

		bin, err := resolveCliproxyBin(cctx.String("bin"))
		if err != nil {
			return err
		}

		flag := "-" + provider + "-login"
		if cctx.Bool("device") {
			if provider != "codex" {
				return fmt.Errorf("--device is only supported for codex; %s uses the browser flow", provider)
			}
			flag = "-codex-device-login"
		}

		args := []string{flag}
		if configPath := cctx.String("config"); configPath != "" {
			args = append(args, "-config", configPath)
		}

		// Judge the login by what it writes, not by how it exits: CLIProxyAPI
		// can print "authentication failed" and still exit 0.
		authDir := cctx.String("auth-dir")
		if configPath := cctx.String("config"); configPath != "" {
			if fromConfig, err := cliproxy.AuthDirFromConfig(configPath); err == nil {
				authDir = fromConfig
			}
		}
		before := cliproxy.Snapshot(authDir)

		fmt.Printf("%s %s %s\n\n", color.HiBlackString("running"), bin, strings.Join(args, " "))

		// The flow is interactive — it prints a URL and waits. Wire the child
		// straight to this terminal rather than capturing, and pass Ctrl-C
		// through so an abandoned login does not leave the child running.
		cmd := exec.Command(bin, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signals)

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start %s: %w", bin, err)
		}
		go func() {
			for sig := range signals {
				if cmd.Process != nil {
					_ = cmd.Process.Signal(sig)
				}
			}
		}()

		waitErr := cmd.Wait()

		written := cliproxy.ChangedSince(before, cliproxy.Snapshot(authDir))
		if len(written) == 0 {
			if waitErr != nil {
				return fmt.Errorf("login failed and wrote no credential to %s: %w", cliproxy.ExpandPath(authDir), waitErr)
			}
			// The usual case: the flow reported a failure above and exited 0.
			return fmt.Errorf("login wrote no credential to %s — read the output above for why; "+
				"a polling timeout usually just needs retrying", cliproxy.ExpandPath(authDir))
		}

		fmt.Printf("\n%s %s\n", color.GreenString("Login finished:"), strings.Join(written, ", "))
		fmt.Printf("%s\n", color.HiBlackString("CLIProxyAPI watches its auth directory, so no restart is needed."))
		fmt.Printf("%s\n", color.HiBlackString("Confirm with: computing-provider cliproxy status --probe"))
		return nil
	},
}

// resolveCliproxyBin finds the proxy binary, preferring an explicit path, then
// PATH, then the places it is conventionally unpacked.
func resolveCliproxyBin(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("no CLIProxyAPI binary at %s: %w", explicit, err)
		}
		return explicit, nil
	}
	if path, err := exec.LookPath("CLIProxyAPI"); err == nil {
		return path, nil
	}
	candidates := []string{"./CLIProxyAPI", "/usr/local/bin/CLIProxyAPI"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "CLIProxyAPI", "CLIProxyAPI"))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("CLIProxyAPI binary not found on PATH — pass --bin /path/to/CLIProxyAPI (see https://github.com/router-for-me/CLIProxyAPI)")
}
