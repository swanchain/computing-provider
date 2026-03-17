package main

import (
	"github.com/urfave/cli/v2"
)

var taskCmd = &cli.Command{
	Name:  "task",
	Usage: "Manage tasks",
	Subcommands: []*cli.Command{
		{
			Name:  "list",
			Usage: "List tasks (placeholder - inference tasks are tracked on Swan Inference)",
			Action: func(cctx *cli.Context) error {
				return nil
			},
		},
	},
}
