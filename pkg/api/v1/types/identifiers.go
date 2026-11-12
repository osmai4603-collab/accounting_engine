package types

import "github.com/google/uuid"

type CompanyID = uuid.UUID
type AccountID = uuid.UUID
type FiscalPeriodID = uuid.UUID
type JournalEntryID = uuid.UUID
type JournalEntryLineID = uuid.UUID

func NewCompanyID() CompanyID                 { return uuid.New() }
func NewAccountID() AccountID                 { return uuid.New() }
func NewFiscalPeriodID() FiscalPeriodID       { return uuid.New() }
func NewJournalEntryID() JournalEntryID       { return uuid.New() }
func NewJournalEntryLineID() JournalEntryLineID { return uuid.New() }
