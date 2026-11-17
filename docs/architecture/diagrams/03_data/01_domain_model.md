# مخطط نموذج المجال المحاسبي (Domain Model — Class Diagram)

- **النطاق (Scope):** الكيانات المحاسبية الأساسية والعلاقات المجردة في نموذج المجال بلغة Go بعيدًا عن التفاصيل الفيزيائية لقواعد البيانات.
- **مصدر الحقيقة (Source of Truth):** `internal/posting`, `internal/masterdata`, `internal/subledgers`, `internal/governance`, `pkg/api/v1/types`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: فئات نموذج المجال (Domain Classes)

```mermaid
classDiagram
    class Company {
        +UUID ID
        +string Code
        +string LegalName
        +string FunctionalCurrency
        +string PresentationCurrency
        +bool Active
    }

    class Account {
        +UUID ID
        +UUID CompanyID
        +string Code
        +string Name
        +AccountType Type
        +NormalBalance Normal
        +string CurrencyID
        +bool IsControlAccount
        +bool IsReconcilable
        +bool Active
        +CanPost() bool
    }

    class FiscalPeriod {
        +UUID ID
        +UUID CompanyID
        +UUID FiscalYearID
        +int Number
        +PeriodState State
        +PeriodType Type
        +Time StartDate
        +Time EndDate
        +IsOpen() bool
    }

    class Journal {
        +UUID ID
        +UUID CompanyID
        +string Code
        +string Name
        +JournalType Type
        +int64 CurrentSequence
    }

    class JournalEntry {
        +UUID ID
        +UUID CompanyID
        +UUID JournalID
        +UUID PeriodID
        +Time EntryDate
        +int64 SequenceNumber
        +EntryStatus Status
        +EntryClassification Classification
        +UUID ReversedEntryID
        +string CreatedBy
        +string PostedBy
        +Time PostedAt
        +List~JournalEntryLine~ Lines
        +Validate() error
    }

    class JournalEntryLine {
        +UUID ID
        +UUID MoveID
        +UUID AccountID
        +UUID PartnerID
        +UUID CostCenterID
        +Money TransactionAmount
        +Money FunctionalDebit
        +Money FunctionalCredit
        +Decimal ExchangeRate
        +string MatchingNumber
        +Validate() error
    }

    class CostCenter {
        +UUID ID
        +UUID CompanyID
        +string Code
        +string Name
        +bool Active
    }

    class SubledgerOpenLine {
        +UUID ID
        +UUID CompanyID
        +UUID JournalEntryID
        +UUID PartnerID
        +UUID ControlAccountID
        +string Kind
        +Money OriginalAmount
        +Money ReconciledAmount
        +RemainingBalance() Money
    }

    class AuditRecord {
        +int64 ID
        +UUID CompanyID
        +string EntityType
        +UUID EntityID
        +string Action
        +UUID ActorID
        +string PreviousHash
        +string CurrentHash
        +Time Timestamp
    }

    class EconomicEvent {
        +UUID ID
        +UUID CompanyID
        +string SourceSystem
        +string IdempotencyKey
        +string EventType
        +string PayloadHash
        +EventStatus Status
    }

    %% علاقات الارتباط والاحتواء
    Company "1" --> "*" Account : تمتلك
    Company "1" --> "*" FiscalPeriod : تتبع لها
    Company "1" --> "*" Journal : تدير
    Company "1" --> "*" CostCenter : تحتوي

    Journal "1" --> "*" JournalEntry : يسجل فيه
    FiscalPeriod "1" --> "*" JournalEntry : يتبع لفترة
    JournalEntry "1" *-- "2..*" JournalEntryLine : يتركب من سطور
    Account "1" --> "*" JournalEntryLine : يُرحل عليه
    CostCenter "0..1" --> "*" JournalEntryLine : تخصيص التكلفة

    JournalEntry "1" --> "0..1" JournalEntry : يعكس (ReversedEntryID)
    JournalEntry "1" --> "0..*" SubledgerOpenLine : يولد بند مفتوح
    JournalEntry "1" --> "1" AuditRecord : يولد بصمة تدقيق
    EconomicEvent "1" --> "0..1" JournalEntry : يتحول بعد التقييم إلى
```

---

## 2. مصفوفة الثوابت والأنواع المحاسبية (Enums & Invariants)

| النوع (Type) | القيم المسموحة في الكود | الموقع في الكود |
| --- | --- | --- |
| `AccountType` | `asset`, `liability`, `equity`, `income`, `expense` | `internal/masterdata` |
| `NormalBalance` | `debit`, `credit` | `internal/masterdata` |
| `EntryStatus` | `draft`, `posted`, `reversed` | `internal/posting` |
| `EntryClassification` | `regular`, `adjusting`, `closing`, `opening`, `reversing` | `internal/posting` |
| `PeriodState` | `open`, `soft_closed`, `hard_closed` | `internal/masterdata` |
| `SubledgerKind` | `receivable` (AR), `payable` (AP) | `internal/subledgers` |
| `EventStatus` | `started`, `processing`, `completed`, `retryable_failure`, `failed` | `pkg/spi/events` |
