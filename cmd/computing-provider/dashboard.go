package main

import (
	"fmt"

	"github.com/mitchellh/go-homedir"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/dashboard"
	"github.com/urfave/cli/v2"
)

var dashboardCmd = &cli.Command{
	Name:  "dashboard",
	Usage: "Start the inference dashboard web UI",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "host",
			Usage: "Dashboard listen address (use 0.0.0.0 only on a trusted network)",
			Value: "127.0.0.1",
		},
		&cli.IntFlag{
			Name:  "port",
			Usage: "Dashboard port",
			Value: 3005,
		},
		&cli.StringFlag{
			Name:  "api",
			Usage: "API server address",
			Value: "http://localhost:8085",
		},
	},
	Action: func(cctx *cli.Context) error {
		cpRepoPath, ok := cctx.Context.Value("CP_PATH").(string)
		if !ok {
			var err error
			cpRepoPath, err = homedir.Expand(cctx.String(FlagRepo.Name))
			if err != nil {
				return fmt.Errorf("failed to expand repo path: %v", err)
			}
		}

		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}

		port := cctx.Int("port")
		host := cctx.String("host")
		apiTarget := cctx.String("api")

		// Use config port if not overridden
		if !cctx.IsSet("api") && conf.GetConfig().API.Port > 0 {
			apiTarget = fmt.Sprintf("http://localhost:%d", conf.GetConfig().API.Port)
		}

		_, tokenPath, err := dashboard.EnsureAccessToken(cpRepoPath)
		if err != nil {
			return fmt.Errorf("initialize dashboard access token: %w", err)
		}

		fmt.Printf("Starting Inference Dashboard on http://%s:%d\n", host, port)
		fmt.Printf("API server: %s\n", apiTarget)
		fmt.Printf("Control token: %s\n", tokenPath)

		server := dashboard.NewServer(host, fmt.Sprintf("%d", port), apiTarget)
		return server.Start()
	},
}
