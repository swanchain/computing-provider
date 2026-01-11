package computing

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/robfig/cron/v3"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/constants"
	"github.com/swanchain/computing-provider-v2/internal/models"
)

var NetworkPolicyFlag bool

var deployingChan = make(chan models.Job)
var TaskMap sync.Map

type CronTask struct {
	nodeId string
}

func NewCronTask(nodeId string) *CronTask {
	return &CronTask{nodeId: nodeId}
}

func (task *CronTask) RunTask() {
	task.setFailedUbiTaskStatus()
	task.getUbiTaskReward()
	task.cleanImageResource()
	task.CheckCpBalance()
	task.DeleteSpaceLog()
}

func (task *CronTask) cleanImageResource() {
	if conf.GetConfig().API.AutoDeleteImage {
		c := cron.New(cron.WithSeconds())
		c.AddFunc("0 0/30 * * * ?", func() {
			defer func() {
				if err := recover(); err != nil {
					logs.GetLogger().Errorf("cleanImageResource catch panic error: %+v", err)
				}
			}()
			NewDockerService().CleanResourceForDocker(false)
		})
		c.Start()
	}
}

func (task *CronTask) setFailedUbiTaskStatus() {
	c := cron.New(cron.WithSeconds())
	c.AddFunc("0 0 */10 * * ?", func() {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("task job: [setFailedUbiTaskStatus], error: %+v", err)
			}
		}()

		var taskList []models.TaskEntity
		oneHourAgo := time.Now().Add(-1 * time.Hour).Unix()
		err := NewTaskService().Model(&models.TaskEntity{}).Where("status in (?,?) and create_time <?", models.TASK_RECEIVED_STATUS, models.TASK_RUNNING_STATUS, oneHourAgo).Find(&taskList).Error
		if err != nil {
			logs.GetLogger().Errorf("Failed get task list, error: %+v", err)
			return
		}

		for _, entity := range taskList {
			ubiTask := entity

			if ubiTask.CreateTime < oneHourAgo {
				ubiTask.Status = models.TASK_FAILED_STATUS
			}

			if ubiTask.Contract != "" || ubiTask.BlockHash != "" {
				ubiTask.Status = models.TASK_SUBMITTED_STATUS
			} else {
				ubiTask.Status = models.TASK_FAILED_STATUS
			}

			NewTaskService().SaveTaskEntity(&ubiTask)
		}
	})
	c.Start()
}

func (task *CronTask) getUbiTaskReward() {
	c := cron.New(cron.WithSeconds())
	c.AddFunc("0 */10 * * * ?", func() {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("task job: [GetUbiTaskReward], error: %+v", err)
			}
		}()
		if err := syncTaskStatusForSequencerService(); err != nil {
			logs.GetLogger().Errorf("failed to sync task from sequencer, error: %v", err)
		}
	})
	c.Start()
}

func (task *CronTask) CheckCpBalance() {
	c := cron.New(cron.WithSeconds())
	c.AddFunc("0 0/30 * * * ?", func() {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("check cp balance catch panic error: %+v", err)
			}
		}()
		GetCpBalance()
	})
	c.Start()
}

func (task *CronTask) DeleteSpaceLog() {
	c := cron.New(cron.WithSeconds())
	c.AddFunc("0 0/30 * * * ?", func() {
		defer func() {
			if err := recover(); err != nil {
				logs.GetLogger().Errorf("update container log catch panic error: %+v", err)
			}
		}()

		jobList, err := NewJobService().GetJobList(models.DELETED_FLAG, -1)
		if err != nil {
			logs.GetLogger().Errorf("failed to get job data, error: %+v", err)
			return
		}
		cpRepoPath, _ := os.LookupEnv("CP_PATH")

		for _, job := range jobList {
			if job.CreateTime+int64(24*7*3600) < time.Now().Unix() {
				continue
			}
			if job.ExpireTime+int64(conf.GetConfig().API.ClearLogDuration)*3600 < time.Now().Unix() {
				err := os.RemoveAll(filepath.Join(cpRepoPath, constants.LOG_PATH_PREFIX, job.JobUuid))
				if err != nil {
					logs.GetLogger().Errorf("failed to delete logs, job_uuid: %s, error: %v", job.JobUuid, err)
					continue
				}
			}
		}
	})
	c.Start()
}

type TaskGroup struct {
	Items []*models.TaskEntity
	Ids   []int64
	Uuids []string
	Type  int // 1: contract  2: sequncer 3: mining
}

func handleTasksToGroup(list []*models.TaskEntity) []TaskGroup {
	var groups []TaskGroup
	var group TaskGroup
	var group1 TaskGroup

	const batchSize = 20
	for i := 0; i < len(list); i++ {
		if list[i].Sequencer == 1 {
			if len(group1.Items) > batchSize {
				groups = append(groups, group1)
				group1 = TaskGroup{}
			}
			group1.Items = append(group1.Items, list[i])
			group1.Ids = append(group1.Ids, list[i].Id)
			group1.Type = 2
		} else if list[i].Sequencer == 0 {
			if len(group.Items) > batchSize {
				groups = append(groups, group)
				group = TaskGroup{}
			}
			group.Items = append(group.Items, list[i])
			group.Ids = append(group.Ids, list[i].Id)
			group.Type = 1
		}
	}
	if len(group.Items) > 0 {
		groups = append(groups, group)
	}
	if len(group1.Items) > 0 {
		groups = append(groups, group1)
	}
	return groups
}

func handleTasksToGroupForMining(list []*models.TaskEntity) []TaskGroup {
	var groups []TaskGroup
	var group TaskGroup

	const batchSize = 10
	for i := 0; i < len(list); i++ {
		if len(group.Items) > batchSize {
			groups = append(groups, group)
			group = TaskGroup{}
		}
		group.Items = append(group.Items, list[i])
		group.Uuids = append(group.Uuids, list[i].Uuid)
		group.Type = 3
	}
	if len(group.Items) > 0 {
		groups = append(groups, group)
	}
	return groups
}
