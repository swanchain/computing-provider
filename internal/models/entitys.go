package models

import (
	"encoding/json"
	"gorm.io/gorm"
)

type CpInfoEntity struct {
	Id                 int64    `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeId             string   `json:"node_id" gorm:"node_id"`
	OwnerAddress       string   `json:"owner_address" gorm:"owner_address"`
	Beneficiary        string   `json:"beneficiary" gorm:"beneficiary"`
	WorkerAddress      string   `json:"worker_address" gorm:"worker_address"`
	Version            string   `json:"version" gorm:"version"`
	ContractAddress    string   `json:"contract_address" gorm:"contract_address"`
	MultiAddressesJSON string   `gorm:"multi_addresses_json;type:text" json:"-"`
	TaskTypesJSON      string   `gorm:"task_types_json; type:text" json:"-"`
	CreateAt           string   `json:"create_at" gorm:"create_at"`
	UpdateAt           string   `json:"update_at" gorm:"update_at"`
	MultiAddresses     []string `json:"multi_addresses" gorm:"-"`
	TaskTypes          []uint8  `json:"task_types" gorm:"-"`
}

func (*CpInfoEntity) TableName() string {
	return "t_cp_info"
}

func (c *CpInfoEntity) BeforeSave(tx *gorm.DB) (err error) {
	if len(c.MultiAddresses) != 0 {
		if multiAddrBytes, err := json.Marshal(c.MultiAddresses); err == nil {
			c.MultiAddressesJSON = string(multiAddrBytes)
		} else {
			return err
		}
	}

	if len(c.TaskTypes) != 0 {
		intTaskTypes := make([]int, len(c.TaskTypes))
		for i, v := range c.TaskTypes {
			intTaskTypes[i] = int(v)
		}

		if taskTypesBytes, err := json.Marshal(intTaskTypes); err == nil {
			c.TaskTypesJSON = string(taskTypesBytes)
		} else {
			return err
		}
	}
	return nil
}

func (c *CpInfoEntity) AfterFind(tx *gorm.DB) (err error) {
	if err = json.Unmarshal([]byte(c.MultiAddressesJSON), &c.MultiAddresses); err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(c.TaskTypesJSON), &c.TaskTypes); err != nil {
		return err
	}
	return nil
}
