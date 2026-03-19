package computing

import (
	"github.com/google/wire"
	"github.com/swanchain/computing-provider-v2/internal/db"
	"github.com/swanchain/computing-provider-v2/internal/models"
	"gorm.io/gorm"
)

type CpInfoService struct {
	*gorm.DB
}

func (cpServ CpInfoService) GetCpInfoEntityByAccountAddress(accountAddress string) (*models.CpInfoEntity, error) {
	var cp models.CpInfoEntity
	err := cpServ.Model(&models.CpInfoEntity{}).Where("contract_address=?", accountAddress).Find(&cp).Error
	return &cp, err
}

func (cpServ CpInfoService) SaveCpInfoEntity(cp *models.CpInfoEntity) (err error) {
	cpServ.Model(&models.CpInfoEntity{}).Where("contract_address =?", cp.ContractAddress).Delete(&models.CpInfoEntity{})
	return cpServ.Save(cp).Error
}

func (cpServ CpInfoService) UpdateCpInfoByNodeId(cp *models.CpInfoEntity) (err error) {
	return cpServ.Model(&models.CpInfoEntity{}).Where("node_id =?", cp.NodeId).Updates(cp).Error
}

var cpInfoSet = wire.NewSet(db.NewDbService, wire.Struct(new(CpInfoService), "*"))
