# 📄 وثيقة تطوير محرك المحاسبة — `accounting_engine`

> **الإصدار:** v0.2.0  
> **المسار الرسمي:** `github.com/osmai4603-collab/accounting_engine`  
> **تاريخ التوثيق:** 2026-11-18  
> **حالة الإنتاج:** GO (100% production ready)

---

## جدول المحتويات

1. [بيان المشكلة (Problem Statement)](#1-بيان-المشكلة-problem-statement)
2. [مواصفات المتطلبات (SRS)](#2-مواصفات-المتطلبات-srs)
3. [نموذج المجال (Domain Model)](#3-نموذج-المجال-domain-model)
4. [كتالوج القواعد المحاسبية (Business Rules Catalog)](#4-كتالوج-القواعد-المحاسبية-business-rules-catalog)
5. [توصيف المعمارية (Architecture Description)](#5-توصيف-المعمارية-architecture-description)
6. [سجلات القرارات المعمارية (ADR)](#6-سجلات-القرارات-المعمارية-adr)
7. [مخطط المكونات (Component Diagram)](#7-مخطط-المكونات-component-diagram)
8. [توصيف العقود والواجهات (API / Contract Specification)](#8-توصيف-العقود-والواجهات-api--contract-specification)
9. [نموذج البيانات (Data Model)](#9-نموذج-البيانات-data-model)
10. [استراتيجية الاختبارات (Test Strategy)](#10-استراتيجية-الاختبارات-test-strategy)
11. [خطة قياس الجودة (Quality Measurement Plan)](#11-خطة-قياس-الجودة-quality-measurement-plan)
12. [المتطلبات الأمنية (Security Requirements)](#12-المتطلبات-الأمنية-security-requirements)
13. [دليل التشغيل والصيانة (Operations Manual)](#13-دليل-التشغيل-والصيانة-operations-manual)

---

## 1. بيان المشكلة (Problem Statement)

### 1.1 الخلفية

تحتاج الأنظمة المالية إلى نواة محاسبية موثوقة تُنفذ القيد المزدوج (Double-Entry Bookkeeping) بشكل ذري ومتسق، مع دعم معايير التقارير المالية الدولية (IFRS/IAS). غالبية المحركات المحاسبية المتاحة إما:

- **مقفلة على قطاع واحد** (بنكي أو تجاري) ولا يمكن إعادة استخدامها.
- **تخلط منطق التطبيق بالتخزين**، مما يجعل الاختبار والتبديل بين قواعد البيانات مستحيلاً.
- **لا تفرض الثبات والذرية** في مسار الترحيل المالي.
- **تدّعي الامتثال بالمعايير** دون مصفوفة امتثال موثقة.

### 1.2 المشكلة المحددة

بناء **مكتبة Go قابلة للتضمين والنشر** تعمل كنواة محاسبية:

| البُعد | المتطلب |
| -------- | --------- |
| **الصحة المحاسبية** | قيد مزدوج ذري، أرصدة دائمة، إقفال سنوي، تدفقات نقدية |
| **الاستقلالية المعمارية** | فصل تام بين المنطق والتخزين والنقل (Hexagonal Architecture) |
| **الاستهلاك الخارجي** | مشروع Go خارجي يستورد المكتبة دون `replace` أو `internal` |
| **الامتثال المعياري** | IFRS 15, IFRS 9, IFRS 16, IAS 12 بنطاق معلن وقابل للقياس |
| **الأمان** | عزل بيانات متعدد المستأجرين (Multi-tenant RLS)، سلسلة تدقيق مشفرة |
| **الجاهزية التشغيلية** | DR مختبر، rate limiting، أداء مقاس |

### 1.3 النطاق

```
accounting_engine هو المحرك المحاسبي الأساسي ضمن نظام cashflow_system.
يعمل في وضعين:
  1. مكتبة مدمجة (Embedded SDK Library)
  2. خدمة مستقلة (Standalone gRPC/HTTP Service)
```

---

## 2. مواصفات المتطلبات (SRS)

### 2.1 المتطلبات الوظيفية

#### 2.1.1 إدارة دفتر اليومية (Ledger Management)

| الرقم | المتطلب | الحالة |
| ------- | --------- | -------- |
| FR-01 | إنشاء مسودة قيد يومية مع تحقق من البيانات الأساسية | ✅ مكتمل |
| FR-02 | ترحيل القيد ذرياً (Draft → Posted) مع تخصيص رقم تسلسلي | ✅ مكتمل |
| FR-03 | عكس قيد مرحّل بقيد معاكس كامل (Full Reversal) | ✅ مكتمل |
| FR-04 | استرجاع قيد بمعرفه ضمن نطاق الشركة | ✅ مكتمل |
| FR-05 | تصنيف القيود: عادي، تسوية، إقفال، افتتاح، عكس | ✅ مكتمل |
| FR-06 | دعم القيود التسووية (Adjusting Entries) بأنواعها: استحقاق، تأجيل، مخصص، إعادة تصنيف | ✅ مكتمل |

#### 2.1.2 التقارير المالية (Financial Reporting)

| الرقم | المتطلب | الحالة |
| ------- | --------- | -------- |
| FR-07 | ميزان المراجعة (Trial Balance) | ✅ مكتمل |
| FR-08 | الميزانية العمومية (Balance Sheet) | ✅ مكتمل |
| FR-09 | قائمة الدخل (Income Statement) | ✅ مكتمل |
| FR-10 | دفتر الأستاذ العام (General Ledger) | ✅ مكتمل |
| FR-11 | قائمة التدفقات النقدية — الطريقة المباشرة وغير المباشرة (IAS 7 / IFRS 18) | ✅ مكتمل |
| FR-12 | الإفصاحات المهيكلة (Structured Disclosures) | ✅ مكتمل |

#### 2.1.3 استيعاب الأحداث (Event Ingestion)

| الرقم | المتطلب | الحالة |
|-------|---------|--------|
| FR-13 | معالجة أحداث محاسبية (`AccountingEvent`) مع مفتاح عدم التكرار (Idempotency Key) | ✅ مكتمل |
| FR-14 | توجيه الأحداث حسب النوع عبر `Router` مع قواعد قابلة للتسجيل | ✅ مكتمل |
| FR-15 | دعم `JournalDraft` مباشر في الحدث لتجاوز البحث عن التعيين | ✅ مكتمل |

#### 2.1.4 الحوكمة والتدقيق (Governance & Audit)

| الرقم | المتطلب | الحالة |
|-------|---------|--------|
| FR-16 | تسجيل سلسلة تدقيق غير قابلة للتعديل (Hash-chained Audit Trail) | ✅ مكتمل |
| FR-17 | بصمة الموافقة (Approval Fingerprint) لكل قيد مرحّل | ✅ مكتمل |

#### 2.1.5 الإقفال المالي (Financial Close)

| الرقم | المتطلب | الحالة |
| ------- | --------- | -------- |
| FR-18 | إقفال سنوي ذري مع قائمة تحقق آلية (Pre-Close Checklist) | ✅ مكتمل |
| FR-19 | توليد قيد الإقفال (تصفير حسابات النتيجة → الأرباح المبقاة) | ✅ مكتمل |
| FR-20 | توليد قيد الافتتاح للسنة الجديدة | ✅ مكتمل |
| FR-21 | إعادة فتح مقيدة وخاضعة للتدقيق (Audited Reopening) | ✅ مكتمل |
| FR-22 | سياستا إقفال: مباشر إلى الأرباح المبقاة، أو عبر ملخص الدخل | ✅ مكتمل |

#### 2.1.6 الأسيتاذ المساعدة والمحافظ (Subledgers)

| الرقم | المتطلب | الحالة |
|-------|---------|--------|
| FR-23 | أستاذ فرعي للذمم المدينة والدائنة (AR/AP) | ✅ مكتمل |
| FR-24 | أستاذ فرعي للأصول الثابتة (Fixed Assets) | ✅ مكتمل |
| FR-25 | أستاذ فرعي للخزينة (Treasury) | ✅ مكتمل |

### 2.2 المتطلبات غير الوظيفية

| الرقم | المتطلب | القيمة المستهدفة |
| ------- | --------- | ------------------ |
| NFR-01 | تغطية اختبارية | ≥ 54.1% (gate حالي) |
| NFR-02 | زمن استعادة الكوارث (RTO) | ≈ 1.49 ثانية |
| NFR-03 | نقطة استعادة البيانات (RPO) | 0 (صفر فقدان) |
| NFR-04 | عزل البيانات | Row-Level Security على كل جدول |
| NFR-05 | الذرية المالية | ACID مع Serializable عند الحاجة |
| NFR-06 | التوافق العكسي | SemVer 2.0.0 مع فترة إهمال ≥ 1 MINOR |

---

## 3. نموذج المجال (Domain Model)

### 3.1 الكيانات الأساسية (Core Entities)

```mermaid
classDiagram
    class Company {
        +CompanyID id
        +string code
        +string legal_name
        +string functional_currency
        +string presentation_currency
    }
    
    class Account {
        +AccountID id
        +CompanyID company_id
        +string code
        +string name
        +AccountType type
        +NormalBalance normal_balance
        +bool is_reconcilable
        +bool is_control_account
        +CashFlowActivity cash_flow_activity
    }
    
    class FiscalYear {
        +FiscalYearID id
        +CompanyID company_id
        +int year
        +date start_date
        +date end_date
        +string state
    }
    
    class FiscalPeriod {
        +FiscalPeriodID id
        +FiscalYearID year_id
        +int period_number
        +string state
        +string period_type
    }
    
    class Journal {
        +JournalID id
        +CompanyID company_id
        +string code
        +string name
        +string journal_type
    }
    
    class JournalEntry {
        +JournalEntryID id
        +CompanyID company_id
        +JournalID journal_id
        +FiscalPeriodID fiscal_period_id
        +EntryStatus status
        +EntryClassification classification
        +int64 sequence_number
        +string entry_number
    }
    
    class EntryLine {
        +JournalEntryLineID id
        +AccountID account_id
        +Money functional_debit
        +Money functional_credit
        +Money transaction_amount
        +decimal exchange_rate
    }
    
    Company "1" --> "*" Account
    Company "1" --> "*" FiscalYear
    Company "1" --> "*" Journal
    FiscalYear "1" --> "*" FiscalPeriod
    Journal "1" --> "*" JournalEntry
    FiscalPeriod "1" --> "*" JournalEntry
    JournalEntry "1" --> "2..*" EntryLine
    EntryLine "*" --> "1" Account
```

### 3.2 أنواع الحسابات

| النوع | الرصيد الطبيعي | الوصف |
| ------- | --------------- | ------- |
| `asset` | مدين | الأصول — نقد، مدينون، أصول ثابتة |
| `liability` | دائن | الخصوم — دائنون، قروض |
| `equity` | دائن | حقوق الملكية — رأس المال، الأرباح المبقاة |
| `income` | دائن | الإيرادات والمبيعات |
| `expense` | مدين | المصروفات والتكاليف |

### 3.3 حالات القيد (Entry Lifecycle)

```mermaid
stateDiagram-v2
    [*] --> draft : CreateDraft
    draft --> posted : Post
    posted --> reversed : Reverse
    draft --> [*] : Delete
    reversed --> [*]
```

### 3.4 حالات الفترة المالية

```mermaid
stateDiagram-v2
    [*] --> open : Create
    open --> soft_closed : SoftClose
    soft_closed --> hard_closed : HardClose
    hard_closed --> open : AuditedReopen
    soft_closed --> open : Reopen
```

### 3.5 نوع Money — الدقة المالية

```go
type Money struct {
    amount   decimal.Decimal  // shopspring/decimal
    currency string           // ISO 4217 (3 أحرف)
}

const FinancialScale = 4   // دقة العرض والترحيل
const InternalScale  = 12  // دقة الحسابات الوسيطة
const maximumPrecision = 28 // أقصى دقة مسموحة
```

**القواعد:**

- لا يُسمح بعمليات حسابية بين عملتين مختلفتين (Currency Mismatch Guard)
- التقريب المصرفي (Banker's Rounding) عند الترحيل
- تحويل إلى وحدات صغرى (Minor Units) مع فحص الدقة
- Serialization/Deserialization عبر JSON مع فحص صارم

### 3.6 المعرّفات (Identifiers)

جميع المعرّفات من نوع `uuid.UUID` مع Type Safety:

```
CompanyID, AccountID, FiscalYearID, FiscalPeriodID,
JournalID, JournalEntryID, JournalEntryLineID,
PartnerID, CostCenterID, ProjectID, SegmentID, BusinessUnitID
```

---

## 4. كتالوج القواعد المحاسبية (Business Rules Catalog)

### 4.1 ثوابت الترحيل (Posting Invariants)

| الرقم | القاعدة | التنفيذ |
| ------- | --------- | --------- |
| BR-01 | **القيد المزدوج**: مجموع المدين = مجموع الدائن دائماً | `validatePostingEntry()` → `ErrUnbalancedEntry` |
| BR-02 | **الحد الأدنى**: كل قيد يحتوي على سطرين على الأقل | فحص طول `Lines` |
| BR-03 | **الذرية**: الترحيل يتم ضمن معاملة واحدة أو يُلغى بالكامل | `CommitPosting` ACID |
| BR-04 | **التتابع**: الرقم التسلسلي فريد لكل شركة/دفتر يومية | `NextSequence()` داخل المعاملة |
| BR-05 | **الثبات**: القيد المرحّل لا يُعدّل، فقط يُعكس | Triggers + Application Layer |
| BR-06 | **الفترة المفتوحة**: لا ترحيل في فترة مقفلة | `ErrFiscalPeriodClosed` |

### 4.2 قواعد العكس (Reversal Rules)

| الرقم | القاعدة |
|-------|---------|
| BR-07 | لا يُعكس إلا قيد مرحّل (status = `posted`) |
| BR-08 | لا يُعكس القيد مرتين |
| BR-09 | قيد العكس يحمل إشارة عكسية على كل سطر ويربط بالقيد الأصلي |
| BR-10 | عكس القيد ذري مع الترحيل في معاملة واحدة |

### 4.3 قواعد الإقفال السنوي

| الرقم | القاعدة |
| ------- | --------- |
| BR-11 | لا إقفال مع وجود مسودات غير مرحّلة (Blocker) |
| BR-12 | يجب إقفال جميع الفترات الشهرية (1-12) قبل الإقفال السنوي (Blocker) |
| BR-13 | يجب وجود فترة تسوية (Period 13) نشطة (Blocker) |
| BR-14 | الحسابات الوسيطة (Suspense) يجب أن تكون صفرية (Blocker) |
| BR-15 | التطابق بين الأستاذ العام والفرعي (Blocker) |
| BR-16 | تنبيه بإعادة تقييم العملات الأجنبية (Warning — IAS 21) |

### 4.4 قواعد عدم التكرار (Idempotency)

```
Idempotency Key + Company Scope → Claim → Process → Complete/Fail
حالات: started → completed | retryable_failure | terminal_failure
```

| الرقم | القاعدة |
|-------|---------|
| BR-17 | مفتاح عدم التكرار فريد لكل شركة |
| BR-18 | إعادة الإرسال بنفس المفتاح والبيانات تُرجع النتيجة المحفوظة |
| BR-19 | إعادة الإرسال بنفس المفتاح وبيانات مختلفة → `ErrIdempotencyKeyConflict` |
| BR-20 | طلب متزامن بنفس المفتاح → انتظار أو `ErrIdempotencyInProgress` |

### 4.5 معايير IFRS/IAS (نطاق محدود)

| المعيار | الحالة | النطاق المعلن | الاستثناءات |
| --------- | -------- | -------------- | ------------- |
| **IFRS 15** (الإيرادات) | نطاق محدود | الاعتراف بالإيرادات من العقود مع العملاء | متغيرات السعر المعقدة |
| **IFRS 9** (الأدوات المالية) | نطاق محدود | التصنيف الأساسي والقياس | التحوط المعقد |
| **IFRS 16** (الإيجارات) | نطاق محدود | الالتزامات الرأسمالية الأساسية | الاستثناءات القصيرة المدة |
| **IAS 12** (ضرائب الدخل) | نطاق محدود | الضرائب المؤجلة الأساسية | المجموعات الضريبية المعقدة |

> **قاعدة صارمة (ADR-0005):** لا يُدّعى امتثال أوسع من المصفوفة الموثقة. كل وحدة تُعلن نطاقها واستثناءاتها صراحةً.

---

## 5. توصيف المعمارية (Architecture Description)

### 5.1 الأسلوب المعماري

المحرك مبني وفق **Hexagonal Architecture (Ports & Adapters)** مع فصل صارم:

```
┌──────────────────────────────────────────────────┐
│                   Consumers                       │
│        (gRPC, HTTP, CLI, External Go Projects)    │
└──────────────────┬───────────────────────────────┘
                   │
┌──────────────────▼───────────────────────────────┐
│              pkg/sdk (Entry Point)                │
│         EngineBuilder + Builder Pattern           │
│    Fluent Config: WithStoragePorts, WithStandard  │
└──────────────────┬───────────────────────────────┘
                   │
┌──────────────────▼───────────────────────────────┐
│          pkg/spi/application (Use Cases)          │
│   CreateDraft → Validate → Post → Reverse         │
│   YearEndClosing → Reopen                          │
│   Principal + Authorizer + Context                 │
└──────────┬───────────────────┬───────────────────┘
           │                   │
┌──────────▼─────────┐  ┌────▼────────────────────┐
│   pkg/api/v1       │  │   pkg/spi/standards      │
│   (Domain Types)   │  │   (Standard Modules)     │
│   Money, Account,  │  │   IFRS15, IFRS9,         │
│   Entry, Report    │  │   IFRS16, IAS12          │
└──────────┬─────────┘  │   Registry, ModulePorts  │
           │             └────┬────────────────────┘
           │                  │
┌──────────▼──────────────────▼───────────────────┐
│            pkg/spi/storage (Ports)               │
│   DraftStore, EntryReader, PostingWriter,        │
│   SequenceAllocator, MasterDataReader,           │
│   BalanceReader, AuditWriter, IdempotencyStore,  │
│   TransactionRunner, UnitOfWorkRunner            │
└──────────────────┬───────────────────────────────┘
                   │
┌──────────────────▼───────────────────────────────┐
│              Adapters (internal)                   │
│   ┌─────────────┐   ┌──────────────────────────┐ │
│   │   Memory     │   │   PostgreSQL              │ │
│   │   (Testing)  │   │   (Production)            │ │
│   │   Non-durable│   │   RLS + ACID + Triggers   │ │
│   └─────────────┘   └──────────────────────────┘ │
└──────────────────────────────────────────────────┘
```

### 5.2 طبقات الحزم (Package Layers)

| الطبقة | المسار | المسؤولية | يستورد internal؟ |
| -------- | -------- | ----------- | ----------------- |
| **العقود العامة** | `pkg/api/v1/**` | أنواع المجال والتقارير | ❌ أبداً |
| **واجهات مقدم الخدمة** | `pkg/spi/**` | عقود التخزين والمعايير | ❌ أبداً |
| **SDK** | `pkg/sdk` | تركيب المحرك (Composition Root) | ✅ مسموح (بلا تسريب) |
| **التنفيذ الداخلي** | `internal/**` | المنطق المحاسبي والمحولات | — |
| **التخزين العام** | `storage/**` | نقاط دخول المحولات | ✅ مسموح (بلا تسريب) |
| **الخدمة** | `cmd/**` | gRPC/HTTP Server | ✅ مسموح |

### 5.3 مبادئ التصميم الحاكمة

1. **اتجاه واحد للاعتماد**: `root → pkg/sdk → pkg/api|pkg/spi` — لا يُسمح بالعكس.
2. **حزم العقود نقية**: `pkg/api` و `pkg/spi` لا تستوردان `internal` إطلاقاً.
3. **المحرك لا يملك الهوية**: المصادقة والتفويض مسؤولية النظام المضيف.
4. **المعايير اختيارية**: كل وحدة IFRS/IAS تُفعّل فقط عند توفر اعتمادياتها.
5. **الذرية بالعقد**: `CommitPosting` إما ينجح بالكامل أو يفشل بالكامل.
6. **القدرات المعطّلة صريحة**: الخدمة المعطّلة تُرجع `ErrCapabilityNotImplemented` لا `nil`.

---

## 6. سجلات القرارات المعمارية (ADR)

### ADR-0001: نقطة الدخول العامة الواحدة

| الحقل | القيمة |
| ------- | -------- |
| **الحالة** | معتمد |
| **القرار** | `pkg/sdk` هو نقطة الدخول الوحيدة الموصى بها |
| **السبب** | الحزمة الجذرية كانت تُصدّر أنواعاً من `internal/*` مما يمنع الاستيراد الخارجي |
| **التنفيذ** | Root Package → Deprecated Facade يُزال في أول MAJOR release |

### ADR-0002: مسار Go Module وسياسة الإصدار

| الحقل | القيمة |
| ------- | -------- |
| **الحالة** | معتمد |
| **القرار** | `github.com/osmai4603-collab/accounting_engine` كمسار رسمي مع SemVer 2.0.0 |
| **السبب** | المسار القديم `accounting_engine` غير قابل للنشر |
| **السياسة** | MAJOR عند كسر API — MINOR عند إضافة متوافقة — PATCH عند إصلاح |

### ADR-0003: Application Layer واحدة و Ports/Adapters

| الحقل | القيمة |
| ------- | -------- |
| **الحالة** | معتمد |
| **القرار** | طبقة تطبيق واحدة داخل المحرك + Public Ports + Composition Root في SDK |
| **السبب** | المحول (Adapter) كان يملك منطق التطبيق، مما يمنع التبديل الحقيقي |
| **التنفيذ** | `WithinTransaction(ctx, fn)` كعقد محايد — لا `pgx.Tx` في الواجهة |

### ADR-0004: المصادقة والتفويض

| الحقل | القيمة |
| ------- | -------- |
| **الحالة** | معتمد |
| **القرار** | عقد `Principal` موثوق يوفره المضيف + Authorization Port اختياري |
| **السبب** | المحرك مكتبة، لا يجب أن يفرض مزوّد هوية |
| **الضمانات** | RLS + `SET LOCAL app.current_company_id` + فحص العضوية قبل Application Layer |

### ADR-0005: نطاق الوحدات المعيارية

| الحقل | القيمة |
| ------- | -------- |
| **الحالة** | معتمد |
| **القرار** | Capabilities اختيارية بنطاق معلن و Compliance Matrix |
| **السبب** | لا يجوز ادعاء امتثال من اسم حزمة أو نجاح اختبار تقني |
| **الضمانات** | كل وحدة تصرّح بـ Standard, Edition, Scope, Exclusions, Dependencies |

### ADR-0007: حدود استيراد internal

| الحقل | القيمة |
| ------- | -------- |
| **الحالة** | معتمد |
| **القرار** | فصل حزم العقود (ممنوع internal) عن حزم التركيب (مسموح مع منع التسريب) |
| **الأداة** | `scripts/check_public_api_purity.sh` يفرض القاعدة آلياً |

### ADR-0008: AccountType Revenue → Income

| الحقل | القيمة |
|-------|--------|
| **القرار** | `AccountTypeRevenue` مهمَل لصالح `AccountTypeIncome` مع alias متوافق |

---

## 7. مخطط المكونات (Component Diagram)

### 7.1 بنية المحرك الداخلية

```mermaid
flowchart TD
    subgraph SDK["pkg/sdk — Composition Root"]
        EB["EngineBuilder"]
        EI["engineInstance"]
    end

    subgraph API["pkg/api/v1 — Domain Types"]
        TYPES["types: Money, Currency, Identifiers"]
        LEDGER["ledger: Account, Entry, Period"]
        EVENTS["events: AccountingEvent, Router"]
        REPORTS["reporting: TB, BS, IS, GL, CF"]
    end

    subgraph SPI["pkg/spi — Service Provider Interfaces"]
        STORAGE["storage: Ports, DraftStore, PostingWriter..."]
        STANDARDS["standards: StandardModule, Registry, ModulePorts"]
        APPLICATION["application: Service, Commands, Authorizer"]
        SUBLEDGER["subledger: ARAPStores, FixedAssets, Treasury"]
    end

    subgraph INTERNAL["internal — Implementation"]
        POSTING["posting: Engine, Validation, Balance, Lock"]
        SDKAPP["sdkapplication: Application, PublicService"]
        IFRS15["ifrs15: Revenue Recognition"]
        IFRS9["ifrs9: Financial Instruments"]
        IFRS16["ifrs16: Leases"]
        IAS12["ias12: Income Taxes"]
        RECON["reconciliation: Subledger Matching"]
        REPORT_INT["reporting: DisclosureRegistry, Taxonomy"]
        GOVERN["governance: Approval, Audit"]
    end

    subgraph ADAPTERS["Adapters"]
        MEM["memory: In-Memory Store"]
        PG["postgres: PostgreSQL Driver"]
    end

    EB --> EI
    EI --> SDKAPP
    SDKAPP --> POSTING
    SDKAPP --> APPLICATION
    EI --> IFRS15 & IFRS9 & IFRS16 & IAS12
    EI --> REPORT_INT
    POSTING --> STORAGE
    PG --> STORAGE
    MEM --> STORAGE
```

### 7.2 الوحدات الداخلية

| الوحدة | المسار | المسؤولية |
| -------- | -------- | ----------- |
| `posting` | `internal/posting` | محرك الترحيل، التحقق، الأرصدة، القفل |
| `sdkapplication` | `internal/sdkapplication` | طبقة التطبيق، Use Cases |
| `ifrs15` | `internal/ifrs15` | الاعتراف بالإيرادات |
| `ifrs9` | `internal/ifrs9` | الأدوات المالية |
| `ifrs16` | `internal/ifrs16` | عقود الإيجار |
| `ias12` | `internal/ias12` | ضرائب الدخل المؤجلة |
| `reconciliation` | `internal/reconciliation` | مطابقة الأستاذ الفرعي |
| `reporting` | `internal/reporting` | سجل الإفصاحات والتصنيف |
| `governance` | `internal/governance` | الموافقات والتدقيق |
| `valuation` | `internal/valuation` | التقييم وأسعار الصرف |
| `consolidation` | `internal/consolidation` | التوحيد المالي |
| `masterdata` | `internal/masterdata` | البيانات الأساسية |
| `policy` | `internal/policy` | السياسات المحاسبية |
| `events` | `internal/events` | معالجة الأحداث الداخلية |
| `subledgers` | `internal/subledgers` | AR/AP, Fixed Assets, Treasury, Bank Parsers |
| `observability` | `internal/observability` | المراقبة والقياس |
| `ratelimit` | `internal/ratelimit` | تحديد معدل الطلبات |
| `serviceauth` | `internal/serviceauth` | مصادقة الخدمة |
| `portbridge` | `internal/portbridge` | جسر بين المنافذ القديمة والجديدة |
| `legacyengine` | `internal/legacyengine` | المحرك القديم للتوافق |

---

## 8. توصيف العقود والواجهات (API / Contract Specification)

### 8.1 واجهة المحرك الرئيسية (Engine Interface)

```go
type Engine interface {
    Ledger()     LedgerService
    Reporting()  ReportingService
    Governance() GovernanceService
    Events()     EventIngestionService
    Standards()  StandardsService
    Scheduler()  standards.SchedulerPort
    Close()      error
    CloseWithContext(ctx context.Context) error
}
```

### 8.2 خدمة دفتر اليومية (LedgerService)

```go
type LedgerService interface {
    PostEntry(ctx, entry JournalEntry) (JournalEntry, error)
    GetEntry(ctx, companyID, entryID) (JournalEntry, error)
    ReverseEntry(ctx, companyID, entryID, reason) (JournalEntry, error)
}
```

### 8.3 خدمة التقارير (ReportingService)

```go
type ReportingService interface {
    GetTrialBalance(ctx, companyID, asOf, currency) (TrialBalance, error)
    GetBalanceSheet(ctx, companyID, asOf, currency) (BalanceSheetReport, error)
    GetIncomeStatement(ctx, companyID, start, end, currency) (IncomeStatementReport, error)
    GetGeneralLedger(ctx, companyID, accountID, start, end) (GeneralLedgerReport, error)
    GetCashFlow(ctx, request CashFlowRequest) (CashFlowStatementReport, error)
    GetDisclosures(ctx, query DisclosureQuery) ([]StructuredDisclosure, error)
}
```

### 8.4 خدمة استيعاب الأحداث (EventIngestionService)

```go
type EventIngestionService interface {
    ProcessEvent(ctx, event AccountingEvent) (JournalEntry, error)
}
```

### 8.5 خدمة الإقفال السنوي (YearEndClosingService)

```go
type YearEndClosingService interface {
    ExecuteYearEndClosing(ctx, command ExecuteYearEndClosingCommand) (YearEndClosingResult, error)
    ReopenYearEndClosing(ctx, command ReopenYearEndClosingCommand) (ReopenYearEndClosingResult, error)
}
```

### 8.6 عقد Application Layer

```go
type Service interface {
    CreateDraft(ctx, command CreateDraftCommand)   (EntryResult, error)
    Validate(ctx, command ValidateEntryCommand)     error
    Post(ctx, command PostEntryCommand)             (EntryResult, error)
    Reverse(ctx, command ReverseEntryCommand)       (EntryResult, error)
    GetEntry(ctx, query GetEntryQuery)              (JournalEntry, error)
}
```

### 8.7 منافذ التخزين (Storage Ports)

```go
type Ports struct {
    Drafts      DraftStore           // حفظ المسودات
    Entries     EntryReader          // قراءة القيود
    Posting     PostingWriter        // ترحيل ذري
    Sequences   SequenceAllocator    // أرقام تسلسلية
    MasterData  MasterDataReader     // بيانات أساسية
    Balance     BalanceReader        // أرصدة الحسابات
    Audit       AuditWriter          // سجل التدقيق
    Idempotency IdempotencyStore     // عدم التكرار
    TxRunner    TransactionRunner    // إدارة المعاملات
    UOWRunner   UnitOfWorkRunner     // وحدة العمل
    Events      publicevents.Stores  // أحداث خارجية
    Private     PrivateExtensions    // امتدادات خاصة
}
```

### 8.8 عقد الوحدات المعيارية (StandardModule)

```go
type StandardModule interface {
    Name()                string
    Version()             string
    Dependencies()        []string
    ValidateDependencies(ports ModulePorts) error
    Initialize(ctx, ports ModulePorts) error
    RegisterRules(registrar events.RuleRegistrar) error
    RunScheduledJobs(ctx, runner ScheduledJobRunner) error
    Close(ctx) error
}
```

### 8.9 خدمة gRPC

```protobuf
service AccountingService {
    rpc PostEntry (PostEntryRequest) returns (PostEntryResponse);
    rpc GetEntry (GetEntryRequest) returns (GetEntryResponse);
    rpc ReverseEntry (ReverseEntryRequest) returns (ReverseEntryResponse);
    rpc GetTrialBalance (GetTrialBalanceRequest) returns (GetTrialBalanceResponse);
}
```

### 8.10 نمط البناء (Builder Pattern)

```go
// الطريقة الموصى بها لبناء المحرك
engine, err := sdk.NewBuilder(
    sdk.WithStoragePorts(driver.Ports()),
    sdk.WithReporting(driver.ReportingStore()),
    sdk.WithGovernance(driver.GovernanceStore()),
    sdk.WithStandard(ifrs15Module),
    sdk.WithStandard(ifrs9Module),
    sdk.WithSubledgerARAP(arapStores),
    sdk.WithPostingConfig(config),
    sdk.WithAuthorizer(hostAuthorizer),
).Build(ctx)
```

---

## 9. نموذج البيانات (Data Model)

### 9.1 مخطط قاعدة البيانات الأساسي

```mermaid
erDiagram
    currencies ||--o{ companies : "functional_currency"
    companies ||--o{ accounts : "company_id"
    companies ||--o{ fiscal_years : "company_id"
    companies ||--o{ journals : "company_id"
    fiscal_years ||--o{ fiscal_periods : "year_id"
    journals ||--o{ account_moves : "journal_id"
    fiscal_periods ||--o{ account_moves : "period_id"
    account_moves ||--o{ account_move_lines : "move_id"
    accounts ||--o{ account_move_lines : "account_id"
    companies ||--o{ financial_audit_logs : "company_id"
    companies ||--o{ exchange_rates : "company_id"
    companies ||--o{ accounting_configurations : "company_id"
    
    currencies {
        varchar3 code PK
        varchar3 numeric_code
        varchar100 name
        smallint minor_units
    }
    
    companies {
        uuid id PK
        varchar50 code UK
        varchar255 legal_name
        varchar3 functional_currency FK
        varchar3 presentation_currency FK
    }
    
    accounts {
        uuid id PK
        uuid company_id FK
        varchar32 code
        varchar255 name
        varchar50 account_type
        varchar6 normal_balance
        bool is_control_account
        varchar40 cash_flow_activity
        varchar30 ifrs_category
    }
    
    fiscal_years {
        uuid id PK
        uuid company_id FK
        int year
        date start_date
        date end_date
        varchar20 state
    }
    
    fiscal_periods {
        uuid id PK
        uuid company_id FK
        uuid year_id FK
        int period_number
        varchar32 state
        varchar32 period_type
    }
    
    journals {
        uuid id PK
        uuid company_id FK
        varchar32 code
        varchar255 name
        varchar32 journal_type
    }
    
    account_moves {
        uuid id PK
        uuid company_id FK
        uuid journal_id FK
        uuid period_id FK
        int64 sequence_number
        varchar255 entry_number
        varchar32 status
        varchar32 classification
        timestamptz effective_date
    }
    
    account_move_lines {
        uuid line_id PK
        uuid move_id FK
        uuid account_id FK
        numeric28_12 functional_debit
        numeric28_12 functional_credit
        numeric28_4 transaction_amount
        numeric28_10 exchange_rate
    }
    
    financial_audit_logs {
        bigint id PK
        uuid company_id FK
        varchar128 entity_type
        uuid entity_id
        varchar128 action
        uuid actor_id
        varchar64 previous_log_hash
        varchar64 current_log_hash
    }
```

### 9.2 جداول رئيسية إضافية

| الجدول | الغرض |
| -------- | ------- |
| `perpetual_balances` | أرصدة حسابات دائمة محدّثة بالمحفزات |
| `economic_events` | أحداث اقتصادية مع دورة حياة |
| `public_event_ingestion` | سجل عدم تكرار الأحداث |
| `accounting_event_account_mappings` | تعيين الأحداث إلى حسابات |
| `ifrs15_revenue_contracts` | عقود IFRS 15 |
| `subledger_reconciliation_logs` | سجلات مطابقة الأستاذ الفرعي |
| `bank_statements` / `bank_items` | كشوف البنك والبنود |
| `exchange_rates` | أسعار الصرف مع تاريخ السريان |
| `account_reconciliations` | التسويات المالية |
| `bank_reconciliations` | التسويات البنكية |
| `approval_bindings` | ربط الموافقات بالقيود |
| `versioned_policy_units` | سياسات محاسبية مؤرخة |

### 9.3 تاريخ الترحيلات (Migration History)

المحرك يستخدم نظام ترحيل مرقم مع فحص تكامل تشفيري:

| الترحيل | الوصف |
| --------- | ------- |
| `202611010001` | المخطط الأساسي (Baseline) — 20+ جدول |
| `202611100001` | سلامة البيانات (Phase 0) |
| `202611100002` | تصنيف القيود |
| `202611100003` | قواعد الأحداث المحاسبية |
| `202611100004` | حسابات الرقابة |
| `202611100005` | الحوكمة والموافقات |
| `202611100006` | الأبعاد التحليلية |
| `202611100007` | فرض RLS على المستأجرين |
| `202611100008` | عقود IFRS 15 |
| `202611100009` | أرصدة حسابات دائمة |
| `202611100010` | بحث أرصدة فترات التسوية |
| `202611100011` | توحيد طبقات العملات |
| `202611100012` | سجلات مطابقة الأستاذ الفرعي |
| `202611100013` | تصنيفات التقارير |
| `202611110001-006` | إصلاحات وتحسينات المحفزات |
| `202611110030` | دورة حياة الأحداث الاقتصادية |
| `202611110040` | تتبع التقارير |
| `202611120001` | عدم تكرار الأحداث الخارجية |
| `202611130001` | سبب فشل الأحداث |
| `202611140001-006` | عدم تكرار العكس، UoW، دورة الفترات |
| `202611150001` | فرض RLS على الأحداث |
| `202611170001` | موافقة إعادة فتح الفترات |

### 9.4 ضمانات قاعدة البيانات

| الضمانة | التنفيذ |
| --------- | --------- |
| **ACID** | معاملات PostgreSQL مع مستويات عزل قابلة للتكوين |
| **RLS** | سياسات Row-Level Security على كل جدول مع `app.current_company_id` |
| **الثبات** | Triggers تمنع تعديل القيود المرحّلة |
| **التتابع** | `SELECT FOR UPDATE` لحماية الأرقام التسلسلية |
| **السلامة المرجعية** | FOREIGN KEY مع `ON DELETE RESTRICT` |
| **التفرد** | UNIQUE indexes على (company_id, code) لكل كيان |
| **سلسلة التدقيق** | تسلسل هاش تشفيري (hash chain) على سجلات التدقيق |

---

## 10. استراتيجية الاختبارات (Test Strategy)

### 10.1 فئات الاختبارات

| الفئة | المسار | الهدف |
| ------- | -------- | ------- |
| **اختبارات الوحدة** | `**/*_test.go` (مع الكود) | صحة المنطق المحاسبي |
| **تكافؤ العقود** | `test/contract_parity/` | سلوك متطابق بين Memory و PostgreSQL و External |
| **الاستهلاك الخارجي** | `test/external_consumer/` | مشروع Go خارجي يبني بدون `internal` |
| **المحول الخارجي** | `test/external_adapter_consumer/` | adapter خارجي ينفذ `storage.Ports` |
| **ACID** | `test/acid/` | استقلالية الصناعة والبنية التحتية والاستهلاك |
| **الذرية** | `test/atomic/` | ذرية الترحيل |
| **التزامن** | `test/concurrency/` | قفل الترحيل المتزامن |
| **استعادة الكوارث** | `test/dr/` | النسخ الاحتياطي والاسترجاع |
| **Bootstrap** | `test/bootstrap/` | سلامة القدرات عند التهيئة |
| **المعايير** | `test/standards/` | تكافؤ سير عمل الترحيل والمنافذ |
| **التكامل** | `test/integration/` | استعادة الأعطال في الأحداث |
| **الأداء** | `test/benchmark/` | قياسات الأداء |
| **تكامل الخدمة** | `test/service_integration/` | الخدمة المستقلة end-to-end |

### 10.2 نتائج تقرير التكافؤ (Contract Parity Report)

```
Adapters: Memory, External SPI, PostgreSQL
Scenarios: 7 × 3 adapters + 1 cross-adapter = 22 اختبار
النتيجة: 22 نجاح، 0 فشل، 0 تخطّي

السيناريوهات:
✅ CapabilitiesMatrix        — مصفوفة القدرات
✅ CoreLifecycle             — الدورة الأساسية
✅ CrossAdapterSemanticEquiv — تكافؤ دلالي عبر المحولات
✅ EventIdempotency          — عدم تكرار الأحداث
✅ P7-GS-01-CoreDoubleEntry  — القيد المزدوج الأساسي
✅ ReversalLifecycle          — دورة العكس
✅ SemanticErrorsAndInvariants — أخطاء دلالية وثوابت
✅ StandardsPostingWorkflow   — سير عمل ترحيل المعايير
```

### 10.3 بوابات CI

```makefile
ci: fmt-check vet build architecture-check test test-gapred
    api-compat published-module integration-service race
    coverage security migrations
```

| البوابة | الأمر | الهدف |
| --------- | ------- | ------- |
| `fmt-check` | `check_gofmt.sh` | تنسيق الكود |
| `vet` | `go vet ./...` | تحليل ثابت |
| `build` | `go build ./...` | نجاح البناء |
| `architecture-check` | `check_public_api_purity.sh` + `check_application_layer.sh` | نقاء العقود |
| `test` | `go test -count=1 ./...` + external consumers | كل الاختبارات |
| `api-compat` | `check_api_compat.sh` | توافق API عكسي |
| `published-module` | `check_published_module.sh` | قابلية النشر |
| `race` | `go test -race` | سلامة التزامن |
| `coverage` | التغطية ≥ 54.1% | حد أدنى للتغطية |
| `security` | `govulncheck` + `gosec` + `scan_secrets.sh` | المسح الأمني |
| `migrations` | اختبارات الترحيل | سلامة المخطط |

---

## 11. خطة قياس الجودة (Quality Measurement Plan)

### 11.1 معايير الجودة المرجعية

| المعيار | الاستخدام |
| --------- | ----------- |
| **ISO/IEC 25010:2023** | نموذج جودة المنتج — الوظائف، الموثوقية، الصيانة |
| **ISO/IEC/IEEE 42010:2022** | توصيف المعمارية |
| **SemVer 2.0.0** | إدارة الإصدارات |
| **OWASP ASVS 5.0.0** | ضوابط الأمان |
| **IFRS Framework** | الصحة المحاسبية |

### 11.2 مؤشرات الجودة (KPIs)

| المؤشر | القيمة الحالية | الهدف |
| -------- | --------------- | ------- |
| تغطية اختبارية | ≥ 54.1% | رفع تدريجي |
| تكافؤ العقود | 22/22 (100%) | 100% دائماً |
| بوابات CI | 12/12 ناجحة | 100% |
| RTO (استعادة الكوارث) | ≈ 1.49s | ≤ 5s |
| RPO (نقطة الاسترجاع) | 0 | 0 |
| أخطاء أمنية حرجة | 0 | 0 |
| أخطاء `go vet` | 0 | 0 |
| مشاكل Race Condition | 0 | 0 |

### 11.3 بوابات الإصدار (Release Gates)

وفق [Release Readiness Report v0.2.0](../release_readiness_v0.2.0.md):

```
G1  — البناء والتحليل الثابت                    ✅
G2  — الاختبارات الوظيفية                        ✅
G3  — تكافؤ العقود بين المحولات                  ✅
G4  — الاستهلاك الخارجي                         ✅
G5  — توافق API العكسي                          ✅
G6  — سلامة المعمارية                            ✅
G7  — التغطية الاختبارية                         ✅
G8  — المسح الأمني                               ✅
G9  — سلامة الترحيلات                            ✅
G10 — سلامة التزامن (Race Detection)             ✅
G11 — تكامل الخدمة المستقلة                      ✅
G12 — استعادة الكوارث                            ✅
```

---

## 12. المتطلبات الأمنية (Security Requirements)

### 12.1 عزل البيانات (Tenant Isolation)

```sql
-- Row-Level Security على كل جدول
ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON accounts
    USING (company_id = current_setting('app.current_company_id')::uuid);

-- يُضبط داخل المعاملة فقط
SET LOCAL app.current_company_id = '<company-uuid>';
```

**الضمانات:**

- RLS مفعّل على كل الجداول المالية
- الخدمة لا تعمل بدور يتجاوز RLS
- `app.current_company_id` يُضبط `transaction-local` وينتهي مع المعاملة
- اختبارات العزل تشمل تزوير Company ID

### 12.2 سلسلة التدقيق المشفرة

```sql
-- كل سجل تدقيق يحمل hash للسجل السابق
financial_audit_logs (
    previous_log_hash varchar(64) NOT NULL,
    current_log_hash  varchar(64) NOT NULL
)
-- فهارس فريدة تضمن عدم التلاعب
UNIQUE (company_id, previous_log_hash)
UNIQUE (company_id, current_log_hash)
```

### 12.3 ثبات البيانات المالية

- **Triggers**: تمنع UPDATE/DELETE على القيود المرحّلة
- **Application Layer**: تتحقق من الحالة قبل أي تعديل
- **الإقفال**: الفترات المقفلة ترفض أي ترحيل جديد

### 12.4 المصادقة والتفويض

```go
type Principal struct {
    ActorID     string
    TenantID    string
    CompanyIDs  []types.CompanyID
    Permissions []string
}

// إجراءات التفويض
ActionRead, ActionCreate, ActionValidate,
ActionPost, ActionReverse, ActionCreateAdjustment,
ActionClose, ActionReopen
```

**التسلسل:** `Unauthorized` → `Forbidden` → الوصول للبيانات

### 12.5 تحديد معدل الطلبات (Rate Limiting)

| البروتوكول | الحماية |
| ----------- | --------- |
| HTTP | `429 Too Many Requests` |
| gRPC | `RESOURCE_EXHAUSTED` |

### 12.6 الأمان التشغيلي

- `govulncheck` — فحص الثغرات في التبعيات
- `gosec` — تحليل أمني ثابت للكود
- `scan_secrets.sh` — منع تسرب الأسرار في الكود
- `check_licenses.sh` — التحقق من رخص التبعيات
- `go mod verify` — التحقق من سلامة التبعيات

---

## 13. دليل التشغيل والصيانة (Operations Manual)

### 13.1 طرق التشغيل

#### الوضع 1: مكتبة مدمجة (Embedded SDK)

```go
import "github.com/osmai4603-collab/accounting_engine/pkg/sdk"

engine, err := sdk.NewBuilder(
    sdk.WithStoragePorts(postgresDriver.Ports()),
    sdk.WithReporting(postgresDriver.ReportingStore()),
).Build(ctx)
defer engine.Close()

// استخدام الخدمات
result, err := engine.Ledger().PostEntry(ctx, entry)
report, err := engine.Reporting().GetTrialBalance(ctx, companyID, time.Now(), "SAR")
```

#### الوضع 2: خدمة مستقلة (Standalone Service)

```bash
# Docker Compose
docker compose -f deployments/docker-compose.yml up

# تكوين البيئة
DATABASE_DSN=postgres://user:pass@host:5432/db?sslmode=require
GRPC_PORT=50051
HTTP_PORT=8080
```

### 13.2 التبعيات التقنية

| التبعية | الإصدار | الغرض |
| --------- | --------- | ------- |
| Go | 1.26.0+ | لغة البرمجة |
| PostgreSQL | 14+ | قاعدة البيانات الإنتاجية |
| `pgx/v5` | v5.12.0 | برنامج تشغيل PostgreSQL |
| `shopspring/decimal` | v1.6.0 | الدقة المالية (28 رقم) |
| `google/uuid` | v1.6.0 | المعرّفات الفريدة |
| `grpc` | v1.84.0 | خدمات gRPC |
| `protobuf` | v1.36.12 | تسلسل البيانات |
| `aws-sdk-go-v2` | v1.47.8 | تخزين S3 (احتياطي) |

### 13.3 الترحيلات (Database Migrations)

```bash
# تشغيل الترحيلات — يتم آلياً عبر المحرك
# الترحيلات تتبع نموذج Expand/Contract
# كل ترحيل له ملف عكسي (.down.sql)
# المجموع الحالي: 32 ترحيل
```

### 13.4 استعادة الكوارث (Disaster Recovery)

```bash
# النسخ الاحتياطي
pg_dump -Fc accounting_db > backup.dump

# الاسترجاع
pg_restore -d accounting_db backup.dump

# المؤشرات المقاسة
# RTO ≈ 1.49 ثانية
# RPO = 0 (صفر فقدان بيانات)
```

### 13.5 المراقبة والتنبيه

| المكون | الأداة |
| -------- | -------- |
| سلامة المخطط | `TestValidateSchemaHealth` |
| تكافؤ المحولات | `test/contract_parity` |
| سلامة التدقيق | فحص سلسلة الهاش |
| الأداء | `test/benchmark` |

### 13.6 أوامر التشغيل اليومية

```bash
# تشغيل كل بوابات CI
make ci

# الاختبارات فقط
make test

# الاختبارات مع فحص التزامن
make race

# التغطية الاختبارية
make coverage

# المسح الأمني
make security

# فحص الترحيلات
make migrations

# فحص نقاء API
make architecture-check

# توليد Protobuf
make proto
```

---

## الملاحق

### ملحق أ: خريطة الملفات الرئيسية

| الملف | الغرض |
| ------- | ------- |
| [`doc.go`](../../doc.go) | توثيق الحزمة الجذرية |
| [`facade.go`](../../facade.go) | واجهة التوافق المهمَلة |
| [`go.mod`](../../go.mod) | تعريف الوحدة والتبعيات |
| [`Makefile`](../../Makefile) | أوامر CI المحلية |
| [`CHANGELOG.md`](../../CHANGELOG.md) | سجل التغييرات |
| [`RELEASE_NOTES.md`](../../RELEASE_NOTES.md) | ملاحظات الإصدار |

### ملحق ب: هيكل المجلدات

```
accounting_engine/
├── cmd/                           # نقاط الدخول التنفيذية
│   ├── accounting-service/        # خدمة gRPC/HTTP المستقلة
│   └── agy/                       # أداة CLI
├── pkg/                           # الحزم العامة القابلة للاستيراد
│   ├── api/v1/                    # أنواع المجال
│   │   ├── types/                 # Money, Currency, Identifiers, Errors
│   │   ├── ledger/                # Account, Entry, Period
│   │   ├── events/                # AccountingEvent, Router, Rules
│   │   ├── reporting/             # Reports, CashFlow, Disclosure
│   │   └── subledger/             # Subledger Models
│   ├── spi/                       # واجهات مقدم الخدمة
│   │   ├── storage/               # Storage Ports
│   │   ├── standards/             # StandardModule, Registry, ModulePorts
│   │   ├── application/           # Service, Commands, Authorizer
│   │   ├── events/                # Event Stores
│   │   ├── masterdata/            # Master Data Contracts
│   │   ├── subledger/             # AR/AP, Fixed Assets, Treasury
│   │   └── legacy/                # Legacy Store Interface
│   └── sdk/                       # Composition Root
│       └── templates/             # Adapter Templates
├── internal/                      # التنفيذ الداخلي (غير قابل للاستيراد خارجياً)
│   ├── adapters/
│   │   ├── memory/                # محول الذاكرة (للاختبار)
│   │   └── postgres/              # محول PostgreSQL (للإنتاج)
│   │       └── migrations/        # 32 ترحيل SQL
│   ├── posting/                   # محرك الترحيل
│   ├── sdkapplication/            # طبقة التطبيق
│   ├── ifrs15/                    # IFRS 15 — الإيرادات
│   ├── ifrs9/                     # IFRS 9 — الأدوات المالية
│   ├── ifrs16/                    # IFRS 16 — الإيجارات
│   ├── ias12/                     # IAS 12 — ضرائب الدخل
│   ├── reconciliation/            # المطابقة
│   ├── reporting/                 # التقارير الداخلية
│   ├── governance/                # الحوكمة
│   ├── subledgers/                # الأسيتاذ المساعدة
│   ├── valuation/                 # التقييم
│   ├── masterdata/                # البيانات الأساسية
│   ├── policy/                    # السياسات
│   ├── events/                    # الأحداث الداخلية
│   ├── consolidation/             # التوحيد
│   ├── observability/             # المراقبة
│   ├── ratelimit/                 # تحديد المعدل
│   └── serviceauth/               # مصادقة الخدمة
├── storage/                       # نقاط دخول المحولات العامة
│   ├── postgres/                  # postgres.go
│   ├── memory/                    # memory.go
│   └── masterdatacontract/        # contract.go
├── proto/                         # تعريفات Protobuf
│   └── accounting/v1/             # ledger.proto, reporting.proto, events.proto
├── test/                          # اختبارات متخصصة
│   ├── contract_parity/           # تكافؤ العقود
│   ├── external_consumer/         # استهلاك خارجي
│   ├── external_adapter_consumer/ # محول خارجي
│   ├── acid/                      # خصائص ACID
│   ├── atomic/                    # ذرية الترحيل
│   ├── concurrency/               # التزامن
│   ├── dr/                        # استعادة الكوارث
│   ├── bootstrap/                 # سلامة التهيئة
│   ├── standards/                 # المعايير
│   ├── integration/               # التكامل
│   ├── benchmark/                 # الأداء
│   └── service_integration/       # تكامل الخدمة
├── scripts/                       # أدوات CI/CD
├── deployments/                   # Dockerfile, docker-compose.yml
├── docs/                          # الوثائق
│   ├── adr/                       # قرارات معمارية (8 ADRs)
│   ├── accounting/                # وثائق محاسبية
│   ├── architecture/              # وثائق معمارية + مخططات
│   ├── methodology/               # منهجية التطوير
│   └── operations/                # كتب التشغيل
└── .agnets/                       # مهارات الوكيل
```

### ملحق ج: الحدود والقيود المعروفة

1. **المصادقة**: دليل المستخدمين وRBAC التجاري مسؤولية النظام المضيف
2. **Rate Limiting**: داخل العملية (per-process)؛ يحتاج gateway خارجي للتوسع الأفقي
3. **المعايير**: طبقات أتمتة محدودة النطاق وليست شهادة امتثال شاملة
4. **محول الذاكرة**: غير دائم ومخصص للاختبار والCI فقط
5. **التوحيد المالي**: يتطلب تكوين مجموعات وسيطرة ملكية

---

> **هذه الوثيقة مكمّلة لـ [الدليل الهندسي لبناء المحركات البرمجية](./software_engines.md)**، وتجمع جميع الوثائق المقترحة في جدول الوثائق الأصلي ضمن مستند واحد مفصل ومبني على الكود الفعلي للمحرك.
