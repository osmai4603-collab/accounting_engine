package types

import "github.com/google/uuid"

type CompanyID uuid.UUID
type AccountID uuid.UUID
type FiscalYearID uuid.UUID
type FiscalPeriodID uuid.UUID
type JournalID uuid.UUID
type JournalEntryID uuid.UUID
type JournalEntryLineID uuid.UUID
type PartnerID uuid.UUID
type CostCenterID uuid.UUID
type ProjectID uuid.UUID
type SegmentID uuid.UUID
type BusinessUnitID uuid.UUID

func NewCompanyID() CompanyID                   { return CompanyID(uuid.New()) }
func NewAccountID() AccountID                   { return AccountID(uuid.New()) }
func NewFiscalYearID() FiscalYearID             { return FiscalYearID(uuid.New()) }
func NewFiscalPeriodID() FiscalPeriodID         { return FiscalPeriodID(uuid.New()) }
func NewJournalID() JournalID                   { return JournalID(uuid.New()) }
func NewJournalEntryID() JournalEntryID         { return JournalEntryID(uuid.New()) }
func NewJournalEntryLineID() JournalEntryLineID { return JournalEntryLineID(uuid.New()) }
func NewPartnerID() PartnerID                   { return PartnerID(uuid.New()) }
func NewCostCenterID() CostCenterID             { return CostCenterID(uuid.New()) }
func NewProjectID() ProjectID                   { return ProjectID(uuid.New()) }
func NewSegmentID() SegmentID                   { return SegmentID(uuid.New()) }
func NewBusinessUnitID() BusinessUnitID         { return BusinessUnitID(uuid.New()) }
