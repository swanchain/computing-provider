//go:build wireinject
// +build wireinject

package computing

import (
	"github.com/google/wire"
)

func NewCpInfoService() CpInfoService {
	wire.Build(cpInfoSet)
	return CpInfoService{}
}
