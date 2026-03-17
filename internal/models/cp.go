package models

type Account struct {
	OwnerAddress   string
	NodeId         string
	MultiAddresses []string
	TaskTypes      []uint8
	Beneficiary    string
	WorkerAddress  string
	Version        string
	Contract       string
}
