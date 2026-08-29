package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/urfave/cli/v2"
)

var alertsCmd = &cli.Command{
	Name:  "alerts",
	Usage: "Inspect and test alert delivery",
	Subcommands: []*cli.Command{
		alertsTestCmd,
	},
}

var alertsTestCmd = &cli.Command{
	Name:  "test",
	Usage: "Send a test alert through every configured transport",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "message",
			Usage: "Body of the test alert",
			Value: "Test alert from computing-provider. If you are reading this, alerting works.",
		},
	},
	Action: func(cctx *cli.Context) error {
		cpRepoPath, ok := os.LookupEnv("CP_PATH")
		if !ok {
			return fmt.Errorf("missing CP_PATH env, please set export CP_PATH=<YOUR CP_PATH>")
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}
		cfg := conf.GetConfig()

		if !cfg.Alerts.Enabled() {
			fmt.Println()
			color.Yellow("No alert transport is configured.")
			fmt.Println()
			fmt.Println("Add one to config.toml:")
			fmt.Println()
			fmt.Println("  [Alerts]")
			fmt.Println("  WebhookURL = \"https://hooks.example.com/provider\"")
			fmt.Println()
			fmt.Println("    [Alerts.Email]")
			fmt.Println("    Host = \"smtp.gmail.com\"")
			fmt.Println("    Port = 587")
			fmt.Println("    Username = \"you@example.com\"")
			fmt.Println("    From = \"you@example.com\"")
			fmt.Println("    To = [\"you@example.com\"]")
			fmt.Println()
			fmt.Println("Keep the password out of the file with: export SMTP_PASSWORD=...")
			fmt.Println()
			return nil
		}

		if cfg.Alerts.WebhookEnabled() {
			fmt.Printf("Webhook: %s\n", alerts.RedactURL(cfg.Alerts.WebhookURL))
		}
		if cfg.Alerts.Email.Enabled() {
			transport := "STARTTLS"
			if cfg.Alerts.Email.ImplicitTLS() {
				transport = "implicit TLS"
			}
			fmt.Printf("Email:   %s:%d (%s) -> %v\n",
				cfg.Alerts.Email.Host, cfg.Alerts.Email.Port, transport, cfg.Alerts.Email.To)
			if cfg.Alerts.Email.Username != "" && cfg.Alerts.Email.Password == "" {
				color.Yellow("  No password set. Use SMTP_PASSWORD, or Password in config.toml.")
			}
		}
		fmt.Println()

		notifier := alerts.New(cfg.Alerts, computing.GetNodeId(cpRepoPath), cfg.API.NodeName)
		if err := notifier.SendTest(cctx.String("message")); err != nil {
			color.Red(fmt.Sprintf("Delivery failed: %v", err))
			// The most common cause by far, and the least obvious.
			if strings.Contains(err.Error(), "smtp auth") {
				fmt.Println()
				fmt.Println("Most providers require an app password rather than your login password:")
				fmt.Println("  Gmail:   Account -> Security -> 2-Step Verification -> App passwords")
				fmt.Println("  Outlook: Security -> Advanced security options -> App passwords")
				fmt.Println("Then: export SMTP_PASSWORD='<app password>'")
			}
			return cli.Exit("", 1)
		}

		color.Green("Test alert sent.")
		if cfg.Alerts.WebhookEnabled() {
			fmt.Println("The webhook POST is asynchronous; check the receiver and the provider log.")
		}
		return nil
	},
}
