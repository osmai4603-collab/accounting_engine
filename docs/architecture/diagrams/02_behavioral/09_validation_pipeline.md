# مخطط مسار تدقيق وصحة القيد المحاسبي (Validation Pipeline Flowchart)

- **النطاق (Scope):** التسلسل الهرمي الصارم لترتيب فحوصات القيد المحاسبي ومواضع الرفض، مطابقًا لوثيقة `entry_validation_invariants.md`.
- **مصدر الحقيقة (Source of Truth):** `internal/posting/entry_validation.go`, `internal/posting/engine.go`, `docs/accounting/entry_validation_invariants.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: مراحل التدقيق الهرمية

```mermaid
flowchart TD
    classDef check fill:#f8fafc,stroke:#334155,stroke-width:2px,color:#0f172a;
    classDef pass fill:#dcfce7,stroke:#15803d,stroke-width:2px,color:#064e3b;
    classDef reject fill:#fee2e2,stroke:#b91c1c,stroke-width:2px,color:#7f1d1d;
    classDef stage fill:#eff6ff,stroke:#1d4ed8,stroke-width:2px,color:#0f172a;

    Start(["استلام كائن القيد المحاسبي<br/>JournalEntry Draft"]):::stage

    %% المرحلة الأولى: فحص سلامة الرأس
    Start --> CheckHeaderLines{"1. فحص الرأس:<br/>هل يحتوي سطرين على الأقل؟<br/>وهل يوجد مدين ودائن؟"}:::check
    CheckHeaderLines -- لا --> ErrMinLines["رفض: ErrInvalidEntry<br/>(يلزم سطرين على الأقل مدين ودائن)"]:::reject

    CheckHeaderLines -- نعم --> CheckHeaderFields{"2. حقول الرأس الأساسية:<br/>JournalID != Nil؟<br/>تاريخ القيد صالح؟"}:::check
    CheckHeaderFields -- لا --> ErrHeaderFields["رفض: ErrInvalidEntry<br/>(اليومية أو التاريخ غير صالح)"]:::reject

    %% المرحلة الثانية: فحص السطور والعملات
    CheckHeaderFields -- نعم --> CheckLineCurrencies{"3. فحص العملات بالسطور:<br/>رموز ISO صالحة (3 حروف كبيرة)؟<br/>توافق العملات مع المبالغ؟"}:::check
    CheckLineCurrencies -- لا --> ErrCurrencyCode["رفض: ErrInvalidCurrencyCode<br/>(رمز عملة غير مسجل)"]:::reject

    CheckLineCurrencies -- نعم --> CheckLineSides{"4. منطق طرف السطر:<br/>مبلغ المعاملة موجب؟<br/>طرف واحد فقط موجب (مدين أو دائن)؟<br/>لا توجد سطور صفرية أو سالبة؟"}:::check
    CheckLineSides -- لا --> ErrLineSides["رفض: ErrInvalidEntryLine<br/>(السطر يجب أن يكون مدينًا أو دائنًا حصريًا)"]:::reject

    CheckLineSides -- نعم --> CheckExchangeRates{"5. سعر التحويل والتقريب:<br/>ExchangeRate > 0؟<br/>تطابق المبلغ المحول مع المعاملة؟"}:::check
    CheckExchangeRates -- لا --> ErrExchangeRate["رفض: ErrExchangeRateInvalid<br/>(سعر الصرف لا يطابق تحويل السطر)"]:::reject

    %% المرحلة الثالثة: فحص التوازن المحاسبي
    CheckExchangeRates -- نعم --> CheckBalance{"6. توازن القيد بالعملة الوظيفية:<br/>مجموع المدين == مجموع الدائن؟<br/>Σ Functional Debit == Σ Functional Credit"}:::check
    CheckBalance -- لا --> ErrUnbalanced["رفض: ErrUnbalancedEntry<br/>(القيد غير متوازن بالعملة الوظيفية)"]:::reject

    %% المرحلة الرابعة: فحص الأبعاد والسياسات
    CheckBalance -- نعم --> CheckDimensions{"7. فحص مراكز التكلفة والأبعاد:<br/>هل حسابات الإيراد/المصروف<br/>تحتوي مراكز التكلفة الإلزامية؟"}:::check
    CheckDimensions -- لا --> ErrDimensions["رفض: ErrCostCenterRequired<br/>(مطلوب مركز تكلفة لحسابات النتيجة)"]:::reject

    %% المرحلة الخامسة: فحص البيانات الرئيسية والتزامن
    CheckDimensions -- نعم --> CheckMasterData{"8. فحص الشركة والفترة والحساب:<br/>الشركة نشطة؟<br/>الفترة المالية مفتوحة؟<br/>الحساب قابل للترحيل؟"}:::check
    CheckMasterData -- لا --> ErrMasterData["رفض: ErrFiscalPeriodClosed<br/>أو ErrAccountInactive"]:::reject

    %% المرحلة السادسة: تدقيق الصلاحيات والترحيل
    CheckMasterData -- نعم --> CheckMakerChecker{"9. الفصل الرقابي (Maker-Checker):<br/>إذا تجاوز القيد الحد،<br/>هل المرحل يختلف عن المنشئ؟"}:::check
    CheckMakerChecker -- لا --> ErrMakerChecker["رفض: ErrMakerCheckerViolation<br/>(يمنع ترحيل القيد بواسطة منشئه)"]:::reject

    CheckMakerChecker -- نعم --> ReadyToPost(["القيد سليم ومعتمد ومؤهل للترحيل الذري<br/>Proceed to Lock & Transaction"]):::pass
```

---

## 2. جدول الفحوصات ومستوى إنفاذها

| المرحلة | الفحص | مكان الإنفاذ | نوع الخطأ المرتجع |
| --- | --- | --- | --- |
| **1. بنية الرأس** | سطرين على الأقل، وجود مدين ودائن | `entry.Validate()` | `ErrInvalidEntry` |
| **2. اتساق الشركة** | مطابقة معرف الشركة في السطور مع الرأس | `entry.Validate()` | `ErrCompanyMismatch` |
| **3. أطراف السطور** | استبعاد القيم السالبة وتحديد طرف واحد | `line.Validate()` | `ErrInvalidEntryLine` |
| **4. معيار ISO للعملات** | تحقق من الرمز عبر `currency.IsRegistered` | `line.Validate()` | `ErrInvalidCurrencyCode` |
| **5. التوازن المالي** | تطابق إجمالي المدين والدائن الوظيفي | `entry.Validate()` و `Trigger` | `ErrUnbalancedEntry` |
| **6. سياسة التقريب** | تطبيق خوارزمية المصرفيين (Banker's Rounding) | `ValidateWithRounding` | `ErrRoundingDifference` |
| **7. حالة الفترة** | التأكد من أن حالة الفترة `open` | `masterdata.Service` | `ErrFiscalPeriodClosed` |
| **8. أهلية الحساب** | الحساب نشط وليس حساب تجميع أو مقفل | `masterdata.ValidateCanPost` | `ErrAccountInactive` |
| **9. الرقابة الثنائية** | المنشئ ليس هو المرحل للمبالغ الجوهرية | `checkMakerChecker` | `ErrMakerCheckerViolation` |
