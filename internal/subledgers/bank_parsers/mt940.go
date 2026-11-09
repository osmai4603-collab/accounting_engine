package bank_parsers

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var (
	mt940BalancePattern = regexp.MustCompile(`^:6[012][FM]:([CD])(\d{6})([A-Z]{3})([0-9,]+)$`)
	mt940EntryPattern   = regexp.MustCompile(`^:61:(\d{6})(?:\d{4})?([DC])(?:[RS])?([0-9,]+)(.*)$`)
	mt940Reference      = regexp.MustCompile(`//([^/]+)`)
)

type MT940Parser struct{}

func (MT940Parser) Parse(data []byte) (*BankStatement, error) {
	statement := &BankStatement{}
	var current *ParsedBankTx
	hasOpening, hasClosing := false, false
	lines := bytes.Split(data, []byte{'\n'})
	for _, rawLine := range lines {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":25:") {
			account := strings.TrimSpace(strings.TrimPrefix(line, ":25:"))
			if strings.Contains(account, " ") {
				account = strings.Fields(account)[0]
			}
			statement.AccountIBAN = account
			continue
		}
		if strings.HasPrefix(line, ":60F:") || strings.HasPrefix(line, ":60M:") ||
			strings.HasPrefix(line, ":62F:") || strings.HasPrefix(line, ":62M:") {
			match := mt940BalancePattern.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("%w: malformed balance field %q", ErrUnsupportedData, line)
			}
			amount, err := parseAmount(match[4])
			if err != nil {
				return nil, err
			}
			if match[1] == "D" {
				amount = amount.Neg()
			}
			date, err := parseMT940Date(match[2])
			if err != nil {
				return nil, err
			}
			if statement.Currency != "" && statement.Currency != match[3] {
				return nil, fmt.Errorf("%w: mixed currencies", ErrInvalidStatement)
			}
			statement.Currency = match[3]
			if strings.HasPrefix(line, ":60") {
				if hasOpening {
					return nil, fmt.Errorf("%w: multiple opening balances", ErrUnsupportedData)
				}
				statement.OpeningBalance = amount
				hasOpening = true
			} else {
				if hasClosing {
					return nil, fmt.Errorf("%w: multiple closing balances", ErrUnsupportedData)
				}
				statement.ClosingBalance = amount
				statement.StatementDate = date
				hasClosing = true
			}
			continue
		}
		if strings.HasPrefix(line, ":61:") {
			match := mt940EntryPattern.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("%w: malformed transaction field %q", ErrUnsupportedData, line)
			}
			if statement.Currency == "" {
				return nil, fmt.Errorf("%w: transaction precedes opening balance", ErrInvalidStatement)
			}
			amount, err := parseAmount(match[3])
			if err != nil {
				return nil, err
			}
			if match[2] == "D" {
				amount = amount.Neg()
			}
			valueDate, err := parseMT940Date(match[1])
			if err != nil {
				return nil, err
			}
			current = &ParsedBankTx{ValueDate: valueDate, Amount: amount, Currency: statement.Currency}
			if ref := mt940Reference.FindStringSubmatch(match[4]); ref != nil {
				current.ReferenceNumber = strings.TrimSpace(ref[1])
			}
			statement.Transactions = append(statement.Transactions, *current)
			current = &statement.Transactions[len(statement.Transactions)-1]
			if strings.Contains(match[4], "NCHG") || strings.Contains(match[4], "NCOM") {
				current.BankFee = amount.Abs()
			}
			continue
		}
		if strings.HasPrefix(line, ":86:") && current != nil {
			description := strings.TrimSpace(strings.TrimPrefix(line, ":86:"))
			if current.Description != "" {
				current.Description += " "
			}
			current.Description += description
		}
	}
	if !hasOpening || !hasClosing || statement.Currency == "" {
		return nil, fmt.Errorf("%w: opening/closing balances and currency are required", ErrInvalidStatement)
	}
	return statement, nil
}

func parseAmount(value string) (decimal.Decimal, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", ".")
	amount, err := decimal.NewFromString(value)
	if err != nil || amount.IsNegative() {
		return decimal.Zero, fmt.Errorf("%w: invalid amount %q", ErrUnsupportedData, value)
	}
	return amount, nil
}

func parseMT940Date(value string) (time.Time, error) {
	if len(value) != 6 {
		return time.Time{}, ErrUnsupportedData
	}
	year := 2000 + int(value[0]-'0')*10 + int(value[1]-'0')
	if year >= 2070 {
		year -= 100
	}
	date, err := time.Parse("20060102", fmt.Sprintf("%04d%s", year, value[2:]))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid date %q", ErrUnsupportedData, value)
	}
	return date.UTC(), nil
}
