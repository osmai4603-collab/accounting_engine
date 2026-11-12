package events

type PostingRule struct {
	RuleID             string
	EventType          string
	DebitAccountCode   string
	CreditAccountCode  string
	DescriptionTemplate string
}
