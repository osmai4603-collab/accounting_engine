package bank_parsers

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrInvalidStatement = errors.New("bank statement parser: invalid statement")
	ErrUnsupportedData  = errors.New("bank statement parser: unsupported or malformed data")
)

type BankStatement struct {
	AccountIBAN    string
	StatementDate  time.Time
	OpeningBalance decimal.Decimal
	ClosingBalance decimal.Decimal
	Currency       string
	Transactions   []ParsedBankTx
}

type ParsedBankTx struct {
	ReferenceNumber string
	ValueDate       time.Time
	Amount          decimal.Decimal
	Currency        string
	Description     string
	BankFee         decimal.Decimal
}

type StatementParser interface {
	Parse(data []byte) (*BankStatement, error)
}
