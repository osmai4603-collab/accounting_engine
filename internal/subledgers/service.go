package subledgers

import "github.com/google/uuid"

type Service struct {
	Repository  Repository
	creator     JournalCreator
	Receivables *ReceivableLedger
	Payables    *PayableLedger
	FixedAssets *FixedAssetLedger
	Treasury    *TreasuryLedger
	Matching    *MatchingEngine
	EventBridge *EventBridge
}

type CompanySubledgerService struct {
	CompanyID uuid.UUID
	*Service
}

func NewServiceForCompany(repository Repository, creator JournalCreator, companyID uuid.UUID) *Service {
	service := NewService(repository, creator)
	service.Receivables = NewReceivableLedgerForCompany(repository, companyID)
	service.Payables = NewPayableLedgerForCompany(repository, companyID)
	service.FixedAssets = NewFixedAssetLedgerForCompany(repository, creator, companyID)
	service.Treasury = NewTreasuryLedgerForCompany(repository, companyID)
	service.Matching = NewMatchingEngineForCompany(repository, companyID)
	service.EventBridge = NewEventBridgeForCompany(repository, creator, companyID)
	return service
}

func (s *Service) ForCompany(companyID uuid.UUID) *CompanySubledgerService {
	if s == nil || companyID == uuid.Nil {
		return &CompanySubledgerService{CompanyID: companyID}
	}
	return &CompanySubledgerService{
		CompanyID: companyID,
		Service:   NewServiceForCompany(s.Repository, s.creator, companyID),
	}
}

func NewService(repository Repository, creator JournalCreator) *Service {
	return &Service{
		Repository:  repository,
		creator:     creator,
		Receivables: NewReceivableLedger(repository),
		Payables:    NewPayableLedger(repository),
		FixedAssets: NewFixedAssetLedger(repository, creator),
		Treasury:    NewTreasuryLedger(repository),
		Matching:    NewMatchingEngine(repository),
		EventBridge: NewEventBridge(repository, creator),
	}
}
