# تدفق الأموال وتعدد العملات (Money & Multi-Currency Flow — IAS 21)

- **النطاق (Scope):** مسار تحويل القيم المالية عبر طبقات العملات الثلاث: عملة المعاملة (`Transaction`)، والعملة الوظيفية (`Functional`)، وعملة العرض (`Presentation`)، ونقاط إعادة التقييم وسياسة التقريب المحاسبي.
- **مصدر الحقيقة (Source of Truth):** `internal/valuation`, `internal/posting/rounding.go`, `internal/reporting/currency_translation.go`, `docs/accounting/ias21_compliance_matrix.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: طبقات العملة والتحويل المحاسبي

```mermaid
flowchart TD
    classDef tx fill:#f0f9ff,stroke:#0284c7,stroke-width:2px,color:#0f172a;
    classDef func fill:#fefce8,stroke:#ca8a04,stroke-width:2px,color:#0f172a;
    classDef pres fill:#fdf4ff,stroke:#c026d3,stroke-width:2px,color:#0f172a;
    classDef policy fill:#f8fafc,stroke:#334155,stroke-width:2px,color:#0f172a;

    subgraph Layer1 ["1. عملة المعاملة الأصلية (Transaction Currency)"]
        TxAmount["مبلغ المعاملة بالعملة الأجنبية<br/>(Transaction Amount e.g. USD, EUR)<br/>المصدر: الفاتورة، العقد، السند البنكي"]:::tx
    end

    subgraph ConversionProcess ["2. عملية التحويل والتقريب (Conversion & Rounding)"]
        SpotRate["سعر الصرف الفوري التاريخي<br/>Historical Spot Exchange Rate"]:::policy
        RoundingPolicy["سياسة تقريب المصرفيين<br/>Banker's Rounding (Half to Even)<br/><code>RoundEntryWithRoundingPolicy</code>"]:::policy
        DiffSettlement["تسوية فروق الكسور الصغرى<br/>Rounding Difference Account"]:::policy
    end

    subgraph Layer2 ["3. العملة الوظيفية للشركة (Functional Currency)"]
        FuncAmount["القيمة بالعملة الوظيفية الرسمية<br/>(Functional Debit / Credit e.g. SAR)<br/><b>الأستاذ العام وميزان المراجعة يُحفظان بهذه العملة</b>"]:::func
        UnrealizedFX["إعادة التقييم الدوري (IAS 21 FX Revaluation)<br/>للبنود النقدية المتبقية في نهاية كل فترة<br/>توليد قيد: أرباح/خسائر فروق تقييم غير محققة"]:::func
    end

    subgraph Layer3 ["4. عملة العرض والتقارير الموحدة (Presentation Currency)"]
        CTA_Translation["ترجمة القوائم المالية (IAS 21 Translation)<br/>الأصول والخصوم: بسعر الإقفال الفوري<br/>الإيرادات والمصروفات: بمتوسط سعر الفترة<br/>حقوق الملكية: بالأسعار التاريخية"]:::pres
        CTA_Reserve["احتياطي ترجمة العملات الأجنبية<br/>Foreign Currency Translation Reserve (Equity)"]:::pres
        PresentationFS["القوائم المالية النهائية بلغة العرض<br/>(Presentation Financial Statements e.g. USD)"]:::pres
    end

    %% مسارات التحويل
    TxAmount --> SpotRate
    SpotRate --> RoundingPolicy
    RoundingPolicy --> DiffSettlement
    DiffSettlement --> FuncAmount

    FuncAmount -->|نهاية الفترة المالية| UnrealizedFX
    UnrealizedFX -->|تحديث الأستاذ العام| FuncAmount

    FuncAmount --> CTA_Translation
    CTA_Translation --> CTA_Reserve
    CTA_Translation --> PresentationFS
```

---

## 2. قواعد معيار المحاسبة الدولي IAS 21 في الكود

1. **العملة المرجعية للنواة (Authoritative Currency):**
   - كافة حسابات الدفتر العام (`account_move_lines.functional_debit_minor` و `functional_credit_minor`) تُحفظ بالوحدات الصغرى (Integers/Minors) للعملة الوظيفية لتجنب أخطاء الفاصلة العائمة.
2. **سياسة التقريب (Rounding Invariant):**
   - يُطبق تقريب المصرفيين المعتمد (`Round to nearest even integer`).
   - أي فروق كسور متبقية بسبب قسمة العملات تُسوى آليًا في حساب مخصص (`RoundingDifferenceAccountID`).
3. **إعادة التقييم في نهاية الفترة (Period-End Spot Revaluation):**
   - البنود النقدية (Monetary Items كالذمم المدينة والدائنة والأرصدة البنكية بالعملات الأجنبية) يُعاد تقييمها بأسعار الإقفال الفورية.
   - البنود غير النقدية (Non-Monetary Items كالأصول الثابتة والمخزون) تظل مسجلة بسعر الصرف التاريخي لتاريخ اقتنائها.
4. **احتياطي ترجمة العملات (CTA Reserve):**
   - الفروق الناتجة عن ترجمة القوائم المالية من العملة الوظيفية إلى عملة العرض لا تمر عبر قائمة الدخل، بل تُرحل مباشرة إلى الدخل الشامل الآخر وحقوق الملكية (`CTA Reserve`).

