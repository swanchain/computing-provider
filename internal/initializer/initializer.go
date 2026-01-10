package initializer

import (
	"github.com/filswan/go-swan-lib/logs"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/computing"
)

func ProjectInit(cpRepoPath string) {
	if err := conf.InitConfig(cpRepoPath, false); err != nil {
		logs.GetLogger().Fatal(err)
	}
	nodeID := computing.InitComputingProvider(cpRepoPath)

	computing.NewCronTask(nodeID).RunTask()

	// Start ECP2 marketplace integration if enabled
	ecp2Service := computing.NewECP2Service(nodeID, cpRepoPath)
	if err := ecp2Service.Start(); err != nil {
		logs.GetLogger().Errorf("Failed to start ECP2 service: %v", err)
	}
}
