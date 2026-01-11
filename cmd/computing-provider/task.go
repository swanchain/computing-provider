package main

import (
	"fmt"
	"os"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
	"github.com/swanchain/computing-provider-v2/internal/models"
	"github.com/urfave/cli/v2"
)

var taskCmd = &cli.Command{
	Name:  "task",
	Usage: "Manage tasks",
	Subcommands: []*cli.Command{
		taskList,
		taskDetail,
		taskDelete,
	},
}

var taskList = &cli.Command{
	Name:  "list",
	Usage: "List ECP tasks",
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:  "tail",
			Usage: "Show the last number of lines. If not specified, all are displayed by default",
		},
	},
	Action: func(cctx *cli.Context) error {
		cpRepoPath, ok := os.LookupEnv("CP_PATH")
		tailNum := cctx.Int("tail")
		if !ok {
			return fmt.Errorf("missing CP_PATH env, please set export CP_PATH=<YOUR CP_PATH>")
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}
		return ecpTaskList(tailNum)
	},
}

var taskDetail = &cli.Command{
	Name:      "get",
	Usage:     "Get ECP job detail info",
	ArgsUsage: "[job_uuid]",
	Action: func(cctx *cli.Context) error {
		if cctx.NArg() != 1 {
			return fmt.Errorf("incorrect number of arguments, got %d, missing args: job_uuid", cctx.NArg())
		}

		cpRepoPath, ok := os.LookupEnv("CP_PATH")
		if !ok {
			return fmt.Errorf("missing CP_PATH env, please set export CP_PATH=<YOUR CP_PATH>")
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("load config file failed, error: %+v", err)
		}

		jobUuid := cctx.Args().First()

		job, err := computing.NewEcpJobService().GetEcpJobByUuid(jobUuid)
		if err != nil {
			return fmt.Errorf("failed to get job, job_uuid: %s, error: %v", jobUuid, err)
		}

		var jobType = "Mining"
		if job.JobType == models.InferenceJobType {
			jobType = "Inference"
		}

		var taskData [][]string
		taskData = append(taskData, []string{"TASK NAME:", job.Name})
		taskData = append(taskData, []string{"TASK TYPE:", jobType})
		taskData = append(taskData, []string{"CONTAINER NAME:", job.ContainerName})
		taskData = append(taskData, []string{"GPU NAME:", job.GpuName})
		taskData = append(taskData, []string{"GPU INDEX:", job.GpuIndex})
		taskData = append(taskData, []string{"SERVICE URL:", job.ServiceUrl})
		taskData = append(taskData, []string{"PORTS:", job.PortMap})
		taskData = append(taskData, []string{"STATUS:", job.Status})
		taskData = append(taskData, []string{"CREATE TIME:", time.Unix(job.CreateTime, 0).Format("2006-01-02 15:04:05")})

		header := []string{"TASK UUID:", job.Uuid}
		NewVisualTable(header, taskData, []RowColor{}).SetAutoWrapText(false).Generate(false)

		return nil
	},
}

var taskDelete = &cli.Command{
	Name:      "delete",
	Usage:     "Delete an ECP task",
	ArgsUsage: "[job_uuid]",
	Action: func(cctx *cli.Context) error {
		if cctx.NArg() != 1 {
			return fmt.Errorf("incorrect number of arguments, got %d, missing args: job_uuid", cctx.NArg())
		}
		jobUuid := cctx.Args().First()

		cpRepoPath, ok := os.LookupEnv("CP_PATH")
		if !ok {
			return fmt.Errorf("missing CP_PATH env, please set export CP_PATH=<YOUR CP_PATH>")
		}
		if err := conf.InitConfig(cpRepoPath, true); err != nil {
			return fmt.Errorf("failed to load config file, error: %+v", err)
		}

		ecpJobEntity, err := computing.NewEcpJobService().GetEcpJobByUuid(jobUuid)
		if err != nil {
			return fmt.Errorf("failed to get job, job_uuid: %s, error: %v", jobUuid, err)
		}
		containerName := ecpJobEntity.ContainerName
		if err = computing.NewDockerService().RemoveContainerByName(containerName); err != nil {
			return fmt.Errorf("failed to remove container, job_uuid: %s, error: %v", jobUuid, err)
		}
		computing.NewEcpJobService().DeleteContainerByUuid(jobUuid)
		fmt.Printf("job_uuid: %s service successfully deleted \n", jobUuid)
		return nil
	},
}

func ecpTaskList(tailNum int) error {
	var taskData [][]string
	var rowColorList []RowColor

	ecpJobs, err := computing.NewEcpJobService().GetEcpJobsByLimit(tailNum)
	if err != nil {
		return err
	}

	containerStatus, err := computing.NewDockerService().GetContainerStatus()
	if err != nil {
		return err
	}

	for i, entity := range ecpJobs {
		createTime := time.Unix(entity.CreateTime, 0).Format("2006-01-02 15:04:05")
		statusStr := "terminated"
		if status, ok := containerStatus[entity.ContainerName]; ok {
			if entity.Status != "terminated" {
				computing.NewEcpJobService().UpdateEcpJobEntity(entity.Uuid, status)
			}
			statusStr = status
		}
		taskData = append(taskData, []string{entity.Uuid, entity.Name, entity.Image, entity.ContainerName, statusStr, fmt.Sprintf("%.4f", entity.Reward), createTime})
		rowColorList = append(rowColorList, RowColor{
			row:    i,
			column: []int{4},
			color:  getContainerStatusColor(statusStr),
		})
	}
	header := []string{"TASK UUID", "TASK NAME", "IMAGE NAME", "CONTAINER NAME", "CONTAINER STATUS", "REWARD", "CREATE TIME"}
	NewVisualTable(header, taskData, rowColorList).Generate(true)
	return nil
}

func getContainerStatusColor(status string) []tablewriter.Colors {
	var rowColor []tablewriter.Colors
	switch status {
	case "created":
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgYellowColor}}
	case "running":
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgGreenColor}}
	case "removing":
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgRedColor}}
	case "paused":
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgHiMagentaColor}}
	case "exited":
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgHiBlueColor}}
	case "terminated":
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgHiBlueColor}}
	default:
		rowColor = []tablewriter.Colors{{tablewriter.Bold, tablewriter.FgHiCyanColor}}
	}
	return rowColor
}
