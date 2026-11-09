package bank_parsers

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type CAMT053Parser struct{}

type camtDocument struct {
	Message struct {
		Statements []camtStatement `xml:"Stmt"`
	} `xml:"BkToCstmrStmt"`
}

type camtStatement struct {
	CreationDate string `xml:"CreDtTm"`
	Account      struct {
		ID struct {
			IBAN  string `xml:"IBAN"`
			Other struct {
				ID string `xml:"Id"`
			} `xml:"Othr"`
		} `xml:"Id"`
	} `xml:"Acct"`
	Balances []camtBalance `xml:"Bal"`
	Entries  []camtEntry   `xml:"Ntry"`
}

type camtBalance struct {
	Type struct {
		CodeOrProprietary struct {
			Code string `xml:"Cd"`
		} `xml:"CdOrPrtry"`
		Code string `xml:"Cd"`
	} `xml:"Tp"`
	Amount camtAmount `xml:"Amt"`
	Side   string     `xml:"CdtDbtInd"`
	Date   struct {
		Date     string `xml:"Dt"`
		DateTime string `xml:"DtTm"`
	} `xml:"Dt"`
}

type camtAmount struct {
	Currency string `xml:"Ccy,attr"`
	Value    string `xml:",chardata"`
}

type camtEntry struct {
	Amount      camtAmount `xml:"Amt"`
	Side        string     `xml:"CdtDbtInd"`
	BookingDate struct {
		Date     string `xml:"Dt"`
		DateTime string `xml:"DtTm"`
	} `xml:"BookgDt"`
	ValueDate struct {
		Date     string `xml:"Dt"`
		DateTime string `xml:"DtTm"`
	} `xml:"ValDt"`
	EntryReference string `xml:"NtryRef"`
	AccountRef     string `xml:"AcctSvcrRef"`
	Charges        struct {
		Records []struct {
			Amount camtAmount `xml:"Amt"`
		} `xml:"Rcrd"`
	} `xml:"Chrgs"`
	Details []struct {
		Transactions []struct {
			References struct {
				EndToEndID    string `xml:"EndToEndId"`
				InstructionID string `xml:"InstrId"`
			} `xml:"Refs"`
			Remittance struct {
				Unstructured []string `xml:"Ustrd"`
			} `xml:"RmtInf"`
		} `xml:"TxDtls"`
	} `xml:"NtryDtls"`
}

func (CAMT053Parser) Parse(data []byte) (*BankStatement, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var document camtDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedData, err)
	}
	if len(document.Message.Statements) != 1 {
		return nil, fmt.Errorf("%w: expected exactly one statement", ErrInvalidStatement)
	}
	source := document.Message.Statements[0]
	statement := &BankStatement{
		AccountIBAN:  strings.TrimSpace(source.Account.ID.IBAN),
		Transactions: make([]ParsedBankTx, 0, len(source.Entries)),
	}
	if statement.AccountIBAN == "" {
		statement.AccountIBAN = strings.TrimSpace(source.Account.ID.Other.ID)
	}
	if source.CreationDate != "" {
		date, err := parseCAMTDate(source.CreationDate)
		if err != nil {
			return nil, err
		}
		statement.StatementDate = date
	}
	hasOpening, hasClosing := false, false
	for _, balance := range source.Balances {
		code := balance.Type.CodeOrProprietary.Code
		if code == "" {
			code = balance.Type.Code
		}
		amount, err := parseCAMTAmount(balance.Amount)
		if err != nil {
			return nil, err
		}
		amount = signedCAMTAmount(amount, balance.Side)
		switch code {
		case "OPBD", "PRCD":
			if hasOpening {
				return nil, fmt.Errorf("%w: multiple opening balances", ErrInvalidStatement)
			}
			statement.OpeningBalance = amount
			hasOpening = true
		case "CLBD", "CLAV":
			if hasClosing {
				return nil, fmt.Errorf("%w: multiple closing balances", ErrInvalidStatement)
			}
			statement.ClosingBalance = amount
			hasClosing = true
			balanceDate := balance.Date.Date
			if balanceDate == "" {
				balanceDate = balance.Date.DateTime
			}
			if balanceDate != "" {
				statement.StatementDate, err = parseCAMTDate(balanceDate)
				if err != nil {
					return nil, err
				}
			}
		}
		if statement.Currency == "" {
			statement.Currency = strings.ToUpper(balance.Amount.Currency)
		} else if !strings.EqualFold(statement.Currency, balance.Amount.Currency) {
			return nil, fmt.Errorf("%w: balance currencies differ", ErrInvalidStatement)
		}
	}
	for _, entry := range source.Entries {
		amount, err := parseCAMTAmount(entry.Amount)
		if err != nil {
			return nil, err
		}
		valueDate := entry.ValueDate.Date
		if valueDate == "" {
			valueDate = entry.ValueDate.DateTime
		}
		if valueDate == "" {
			valueDate = entry.BookingDate.Date
			if valueDate == "" {
				valueDate = entry.BookingDate.DateTime
			}
		}
		date, err := parseCAMTDate(valueDate)
		if err != nil {
			return nil, err
		}
		transaction := ParsedBankTx{
			ReferenceNumber: strings.TrimSpace(entry.EntryReference),
			ValueDate:       date,
			Amount:          signedCAMTAmount(amount, entry.Side),
			Currency:        strings.ToUpper(entry.Amount.Currency),
		}
		if transaction.ReferenceNumber == "" {
			transaction.ReferenceNumber = strings.TrimSpace(entry.AccountRef)
		}
		for _, detail := range entry.Details {
			for _, tx := range detail.Transactions {
				if transaction.ReferenceNumber == "" {
					transaction.ReferenceNumber = strings.TrimSpace(tx.References.EndToEndID)
					if transaction.ReferenceNumber == "" {
						transaction.ReferenceNumber = strings.TrimSpace(tx.References.InstructionID)
					}
				}
				for _, description := range tx.Remittance.Unstructured {
					if transaction.Description != "" {
						transaction.Description += " "
					}
					transaction.Description += strings.TrimSpace(description)
				}
			}
		}
		for _, charge := range entry.Charges.Records {
			fee, err := parseCAMTAmount(charge.Amount)
			if err != nil {
				return nil, err
			}
			transaction.BankFee = transaction.BankFee.Add(fee.Abs())
		}
		statement.Transactions = append(statement.Transactions, transaction)
	}
	if !hasOpening || !hasClosing || statement.Currency == "" || statement.StatementDate.IsZero() {
		return nil, fmt.Errorf("%w: opening/closing balances, dates and currency are required", ErrInvalidStatement)
	}
	return statement, nil
}

func parseCAMTAmount(value camtAmount) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(strings.TrimSpace(value.Value))
	if err != nil || amount.IsNegative() || len(value.Currency) != 3 {
		return decimal.Zero, fmt.Errorf("%w: malformed monetary amount", ErrUnsupportedData)
	}
	return amount, nil
}

func signedCAMTAmount(amount decimal.Decimal, side string) decimal.Decimal {
	if strings.EqualFold(strings.TrimSpace(side), "DBIT") {
		return amount.Neg()
	}
	return amount
}

func parseCAMTDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("%w: missing transaction date", ErrInvalidStatement)
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: invalid date %q", ErrUnsupportedData, value)
}
