package subledgers

type Service struct {
	Repository  Repository
	Receivables *ReceivableLedger
	Payables    *PayableLedger
	FixedAssets *FixedAssetLedger
	Treasury    *TreasuryLedger
	Matching    *MatchingEngine
	EventBridge *EventBridge
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
