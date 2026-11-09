package bank_parsers

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestMT940Parser(t *testing.T) {
	input := `:20:STATEMENT-1
:25:SA0380000000608010167519
:60F:C261109SAR1000,00
:61:261110D100,50NTRFNONREF//PAY-44
:86:Supplier payment
:62F:C261110SAR899,50`
	statement, err := (MT940Parser{}).Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if statement.AccountIBAN != "SA0380000000608010167519" ||
		statement.Currency != "SAR" ||
		!statement.OpeningBalance.Equal(decimal.NewFromInt(1000)) ||
		!statement.ClosingBalance.Equal(decimal.RequireFromString("899.50")) ||
		len(statement.Transactions) != 1 {
		t.Fatalf("unexpected statement: %+v", statement)
	}
	transaction := statement.Transactions[0]
	if !transaction.Amount.Equal(decimal.RequireFromString("-100.50")) ||
		transaction.ReferenceNumber != "PAY-44" || transaction.Description != "Supplier payment" {
		t.Fatalf("unexpected transaction: %+v", transaction)
	}
}

func TestCAMT053Parser(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt><Stmt><CreDtTm>2026-11-10T09:30:00Z</CreDtTm>
    <Acct><Id><IBAN>SA0380000000608010167519</IBAN></Id></Acct>
    <Bal><Tp><CdOrPrtry><Cd>OPBD</Cd></CdOrPrtry></Tp><Amt Ccy="SAR">1000.00</Amt><CdtDbtInd>CRDT</CdtDbtInd></Bal>
    <Bal><Tp><CdOrPrtry><Cd>CLBD</Cd></CdOrPrtry></Tp><Amt Ccy="SAR">899.50</Amt><CdtDbtInd>CRDT</CdtDbtInd><Dt><Dt>2026-11-10</Dt></Dt></Bal>
    <Ntry><Amt Ccy="SAR">100.50</Amt><CdtDbtInd>DBIT</CdtDbtInd>
      <ValDt><Dt>2026-11-10</Dt></ValDt><NtryRef>ENTRY-1</NtryRef>
      <NtryDtls><TxDtls><Refs><EndToEndId>PAY-44</EndToEndId></Refs><RmtInf><Ustrd>Supplier payment</Ustrd></RmtInf></TxDtls></NtryDtls>
      <Chrgs><Rcrd><Amt Ccy="SAR">1.00</Amt></Rcrd></Chrgs>
    </Ntry>
  </Stmt></BkToCstmrStmt>
</Document>`
	statement, err := (CAMT053Parser{}).Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if statement.AccountIBAN != "SA0380000000608010167519" ||
		!statement.OpeningBalance.Equal(decimal.NewFromInt(1000)) ||
		!statement.ClosingBalance.Equal(decimal.RequireFromString("899.50")) ||
		len(statement.Transactions) != 1 {
		t.Fatalf("unexpected statement: %+v", statement)
	}
	transaction := statement.Transactions[0]
	if !transaction.Amount.Equal(decimal.RequireFromString("-100.50")) ||
		!transaction.BankFee.Equal(decimal.NewFromInt(1)) ||
		transaction.ReferenceNumber != "ENTRY-1" ||
		transaction.Description != "Supplier payment" {
		t.Fatalf("unexpected transaction: %+v", transaction)
	}
}

func TestBankStatementParsersRejectMalformedInputs(t *testing.T) {
	if _, err := (MT940Parser{}).Parse([]byte(":60F:C261109SARnot-money\n:62F:C261109SAR0,00")); err == nil {
		t.Fatal("MT940 parser accepted malformed amount")
	}
	if _, err := (CAMT053Parser{}).Parse([]byte("<not-xml")); err == nil {
		t.Fatal("CAMT parser accepted malformed XML")
	}
}
