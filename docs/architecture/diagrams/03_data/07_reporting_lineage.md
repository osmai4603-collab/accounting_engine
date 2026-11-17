# مخطط تسلسل وتتبع بيانات التقارير المالية (Financial Reporting Lineage)

- **النطاق (Scope):** مسار تتبع الأرقام المالية من الحدث الاقتصادي الأصلي مرورًا بالقيود والأرصدة الدائمة وصولًا إلى بنود القوائم المالية المعتمدة (Financial Statement Traceability).
- **مصدر الحقيقة (Source of Truth):** `internal/reporting/traceability.go`, `internal/reporting/trial_balance.go`, `internal/reporting/consolidated_bundle.go`, `docs/accounting/financial_reporting_scope.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: شجرة نسب التقارير المالية (Data Lineage Tree)

```mermaid
flowchart TD
    classDef origin fill:#f8fafc,stroke:#475569,stroke-width:2px,color:#0f172a;
    classDef moves fill:#eff6ff,stroke:#2563eb,stroke-width:2px,color:#0f172a;
    classDef ledger fill:#fefce8,stroke:#ca8a04,stroke-width:2px,color:#0f172a;
    classDef fs fill:#ecfdf5,stroke:#059669,stroke-width:2px,color:#0f172a;
    classDef bundle fill:#fdf4ff,stroke:#c026d3,stroke-width:2px,color:#0f172a;

    subgraph Level1 ["1. أصل المعاملات (Source Economic Events)"]
        Ev1["حدث بيع إلكتروني<br/>E-Commerce Invoice Event"]:::origin
        Ev2["سند دفع بنكي<br/>Bank Settlement Payment"]:::origin
        Ev3["عقد إيجار IFRS 16<br/>Lease Agreement Contract"]:::origin
    end

    subgraph Level2 ["2. القيود اليومية وسطورها (Journal Moves & Lines)"]
        Move1["قيد مبيعات وضريبة<br/>Move: REV-2026-001<br/>Lines: AR (Dr) / Sales (Cr) / VAT (Cr)"]:::moves
        Move2["قيد تحصيل بنكي<br/>Move: BNK-2026-042<br/>Lines: Bank (Dr) / AR (Cr)"]:::moves
        Move3["قيد إهلاك أصل الإيجار<br/>Move: DEP-2026-012<br/>Lines: Depr (Dr) / AccDepr (Cr)"]:::moves
    end

    subgraph Level3 ["3. الأستاذ الدائم وميزان المراجعة (Ledger & Trial Balance)"]
        Balances[("الأرصدة التراكمية الدائمة<br/>account_balances<br/>(Beginning + Period = Ending)")]:::ledger
        TB["ميزان المراجعة متعدد العملات<br/><b>Multi-Currency Trial Balance</b><br/>Σ Debit == Σ Credit"]:::ledger
    end

    subgraph Level4 ["4. القوائم المالية المستقلة (Core Financial Statements)"]
        direction TB
        BS["قائمة المركز المالي (Balance Sheet)<br/>الأصول = الخصوم + حقوق الملكية"]:::fs
        PL["قائمة الدخل الشامل (P&L / Comprehensive Income)<br/>الإيرادات - المصروفات = صافي الدخل"]:::fs
        CF["قائمة التدفقات النقدية (Cash Flow Statement)<br/>تشغيلي + استثماري + تمويلي"]:::fs
        EQ["قائمة التغير في حقوق الملكية (Changes in Equity)<br/>رأس المال + الاحتياطيات + الأرباح المبقاة"]:::fs
        Notes["إفصاحات وإيضاحات القوائم (Notes & Disclosures)<br/>تفاصيل السياسات والمخصصات"]:::fs
    end

    subgraph Level5 ["5. الدمج المالي للمجموعة (Consolidated Financial Bundle)"]
        Eliminations["قيود الاستبعاد البينية (Elimination Entries)<br/>استبعاد الاستثمارات والمبيعات البينية"]:::bundle
        ConsolidatedFS["حزمة التقارير المالية الموحدة<br/><b>Consolidated Financial Bundle</b>"]:::bundle
    end

    %% مسارات النسب والتتبع
    Ev1 --> Move1
    Ev2 --> Move2
    Ev3 --> Move3

    Move1 --> Balances
    Move2 --> Balances
    Move3 --> Balances

    Balances --> TB

    TB -->|حسابات الأصول والخصوم| BS
    TB -->|حسابات الإيراد والمصروف| PL
    TB -->|حسابات النقدية وما يعادلها| CF
    PL -->|ترحيل صافي الربح إلى الأرباح المبقاة| EQ
    BS --> Notes
    PL --> Notes

    BS --> Eliminations
    PL --> Eliminations
    Eliminations --> ConsolidatedFS
```

---

## 2. محرك التتبع العكسي (Financial Traceability Engine)

تتيح واجهة `FinancialTraceabilityRepository` للمدقق المالي الإجابة بدقة برمجية عن سؤال:  
**"من أين جاء هذا الرقم في القائمة المالية؟"**

```go
type TraceResult struct {
    StatementItem   string                  // اسم البند في القائمة (مثل: إيرادات العقود)
    AccountIDs      []uuid.UUID             // الحسابات المرتبطة في شجرة الحسابات
    MoveLines       []TracedMoveLine        // سطور القيود الفعلية المساهمة في الرقم
    JournalEntries  []posting.JournalEntry  // رؤوس القيود المعتمدة وتواريخها
    SourceEvents    []TracedEconomicEvent   // الأحداث الاقتصادية الأصلية ومفاتيح التكرار
    StatementAmount decimal.Decimal         // القيمة المجمعة في القائمة المالية
    LedgerAmount    decimal.Decimal         // مجموع أرصدة الأستاذ الفعلي
    Reconciled      bool                    // هل يتطابق رقم القائمة مع الأستاذ 100%؟
    AuditChainProof string                  // إثبات التشفير من سلسلة SHA-256
}
```

### ضمانات التدقيق

1. **تطابق بنسبة 100% (`Reconciled == true`):** لا يمكن إظهار رقم في القائمة المالية لا يوجد له سند مباشر في `account_move_lines`.
2. **سلسلة إثبات غير منقطعة (Cryptographic Proof):** كل سطر في القائمة المالية مدعوم برقم قيد معتمد ومربوط بكتلة تدقيق مشفرة بـ SHA-256.
