package bank_parsers

import "testing"

// FuzzMT940ParserNeverPanics feeds arbitrary financial text, malformed SWIFT
// tags and hostile amounts into the MT940 parser. A parser error is expected
// and fine; a panic is a release blocker.
func FuzzMT940ParserNeverPanics(f *testing.F) {
	seeds := [][]byte{
		[]byte(`:20:STATEMENT-1
:25:SA0380000000608010167519
:60F:C261109SAR1000,00
:61:261110D100,50NTRFNONREF//PAY-44
:86:Supplier payment
:62F:C261110SAR899,50`),
		[]byte(":60F:C261109SARnot-money\n:62F:C261109SAR0,00"),
		[]byte(":61:999999D9999999999999999999999999999999999999"),
		[]byte(":60F:C000000XXX0,00\n:61:000000C1,00\n:62F:C000000XXX0,00"),
		[]byte(":25:\x00\x01\x02\n:60F:C261109USD1,00\n:62F:C261109USD1,00"),
		[]byte("\xff\xfe:61:\n"),
		[]byte(""),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		statement, err := (MT940Parser{}).Parse(data)
		if err != nil {
			return
		}
		if statement == nil {
			t.Fatal("parser returned nil statement without an error")
		}
		for _, transaction := range statement.Transactions {
			_ = transaction.Amount.String()
			_ = transaction.BankFee.String()
		}
	})
}

// FuzzCAMT053ParserNeverPanics feeds arbitrary XML-ish financial text into the
// CAMT.053 parser, including truncated documents and hostile amounts.
func FuzzCAMT053ParserNeverPanics(f *testing.F) {
	seeds := [][]byte{
		[]byte(`<?xml version="1.0" encoding="UTF-8"?>
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
</Document>`),
		[]byte("<not-xml"),
		[]byte(`<Document><BkToCstmrStmt><Stmt><Bal><Amt Ccy="USD">not-a-number</Amt></Bal></Stmt></BkToCstmrStmt></Document>`),
		[]byte(`<Document><BkToCstmrStmt><Stmt><Ntry><Amt Ccy="XXX">9999999999999999999999999999999999999999</Amt><ValDt><Dt>0000-00-00</Dt></ValDt></Ntry></Stmt></BkToCstmrStmt></Document>`),
		[]byte("\xff\xfe\xfd"),
		[]byte(""),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		statement, err := (CAMT053Parser{}).Parse(data)
		if err != nil {
			return
		}
		if statement == nil {
			t.Fatal("parser returned nil statement without an error")
		}
		for _, transaction := range statement.Transactions {
			_ = transaction.Amount.String()
			_ = transaction.BankFee.String()
		}
	})
}
