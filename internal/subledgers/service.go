package subledgers

import "github.com/google/uuid"

type Service struct {
	Repository  Repository
	Receivables *ReceivableLedger
	Payables    *PayableLedger
	FixedAssets *FixedAssetLedger
	Treasury    *TreasuryLedger
	Matching    *MatchingEngine
	EventBridge *EventBridge
}

func NewServiceForCompany(repository Repository, creator JournalCreator, companyID uuid.UUID) *Service {
	service := NewService(repository, creator)
	service.Receivables = NewReceivableLedgerForCompany(repository, companyID)
	service.Payables = NewPayableLedgerForCompany(repository, companyID)
	service.FixedAssets = NewFixedAssetLedgerForCompany(repository, creator, companyID)
	service.Treasury = NewTreasuryLedgerForCompany(repository, companyID)
	service.Matching = NewMatchingEngineForCompany(repository, companyID)
	return service
}
func NewService(repository Repository, creator JournalCreator) *Service {
	return &Service{
		Repository:  repository,
		Receivables: NewReceivableLedger(repository),
		Payables:    NewPayableLedger(repository),
		FixedAssets: NewFixedAssetLedger(repository, creator),
		Treasury:    NewTreasuryLedger(repository),
		Matching:    NewMatchingEngine(repository),
		EventBridge: NewEventBridge(repository, creator),
	}
}
