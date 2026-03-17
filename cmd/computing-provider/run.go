package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/urfave/cli/v2"
)

var infoCmd = &cli.Command{
	Name:  "info",
	Usage: "Print computing-provider info",
	Action: func(cctx *cli.Context) error {
		cpRepoPath, ok := os.LookupEnv("CP_PATH")
		if !ok {
			return fmt.Errorf("missing CP_PATH env, please set export CP_PATH=<YOUR CP_PATH>")
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}

		localNodeId := computing.GetNodeId(cpRepoPath)

		var domain = conf.GetConfig().API.Domain
		if strings.HasPrefix(domain, ".") {
			domain = domain[1:]
		}

		var taskData [][]string
		taskData = append(taskData, []string{"   Name:", conf.GetConfig().API.NodeName})
		taskData = append(taskData, []string{"   Node ID:", localNodeId})
		taskData = append(taskData, []string{"   Domain:", domain})
		taskData = append(taskData, []string{"   Multi-Address:", conf.GetConfig().API.MultiAddress})
		taskData = append(taskData, []string{""})
		taskData = append(taskData, []string{"Inference:"})
		taskData = append(taskData, []string{"   Enabled:", fmt.Sprintf("%v", conf.GetConfig().Inference.Enable)})
		taskData = append(taskData, []string{"   WebSocket URL:", conf.GetConfig().Inference.WebSocketURL})
		taskData = append(taskData, []string{"   Models:", strings.Join(conf.GetConfig().Inference.Models, ", ")})

		header := []string{"CP Info:"}
		NewVisualTable(header, taskData, []RowColor{}).SetAutoWrapText(false).Generate(false)

		return nil
	},
}

var stateCmd = &cli.Command{
	Name:  "state",
	Usage: "Print computing-provider state",
	Action: func(cctx *cli.Context) error {
		cpRepoPath, ok := os.LookupEnv("CP_PATH")
		if !ok {
			return fmt.Errorf("missing CP_PATH env, please set export CP_PATH=<YOUR CP_PATH>")
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}

		localNodeId := computing.GetNodeId(cpRepoPath)

		var taskData [][]string
		taskData = append(taskData, []string{"   Node ID:", localNodeId})
		taskData = append(taskData, []string{"   Name:", conf.GetConfig().API.NodeName})
		taskData = append(taskData, []string{"   Inference Enabled:", fmt.Sprintf("%v", conf.GetConfig().Inference.Enable)})

		header := []string{"CP State:"}
		NewVisualTable(header, taskData, []RowColor{}).SetAutoWrapText(false).Generate(false)

		return nil
	},
}

var initCmd = &cli.Command{
	Name:  "init",
	Usage: "Initialize a new cp",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "multi-address",
			Usage: "The multiAddress for libp2p (optional for Inference mode, defaults to localhost)",
		},
		&cli.StringFlag{
			Name:  "node-name",
			Usage: "The name of cp",
		},
		&cli.IntFlag{
			Name:  "port",
			Usage: "The cp listens on port",
			Value: 9085,
		},
	},
	Action: func(cctx *cli.Context) error {
		multiAddr := cctx.String("multi-address")
		port := cctx.Int("port")
		// Multi-address is optional for Inference mode - default to localhost
		if strings.TrimSpace(multiAddr) == "" {
			multiAddr = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port)
		}
		nodeName := cctx.String("node-name")

		cpRepoPath, _ := os.LookupEnv("CP_PATH")
		return conf.GenerateAndUpdateConfigFile(cpRepoPath, strings.TrimSpace(multiAddr), nodeName, port)
	},
}
