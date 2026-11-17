# مخطط تتابع الإقفال السنوي وإعادة تقييم العملات (Sequence: Year-End Close & FX Revaluation)

- **النطاق (Scope):** المسار الزمني لقائمة التحقق الآلية قبل الإقفال، إعادة تقييم العملات الأجنبية (IAS 21)، توليد قيد الإقفال السنوي في فترة التسوية (Period 13)، وتوليد قيد الافتتاح للسنة الجديدة.
- **مصدر الحقيقة (Source of Truth):** `internal/reporting/closing_engine.go`, `internal/reporting/closing_checklist.go`, `docs/accounting/closing_and_reopening_policy.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: دورة الإقفال المالي السنوي

```mermaid
sequenceDiagram
    autonumber
    actor CFO as المسؤول المالي / CFO
    participant CloseService as محرك الإقفال / Closing Engine
    participant Checklist as قائمة التحقق الآلية / Checklist
    participant FXEngine as محرك تقييم العملات / FX Valuation (IAS 21)
    participant GL as محرك الترحيل / GL Core
    participant DB as قاعدة البيانات / Postgres Store

    CFO->>CloseService: CloseFiscalYearWithChecklist(companyID, fiscalYearID)
    activate CloseService

    %% 1. قائمة التحقق التلقائية (Checklist Pre-checks)
    CloseService->>Checklist: EvaluateClosingChecklist(companyID, fiscalYearID)
    activate Checklist
    Checklist->>DB: فحص وجود قيود مسودة (Draft Entries)
    Checklist->>DB: فحص إقفال كافة الفترات الشهرية 1-12
    Checklist->>DB: فحص تصفير الحسابات الوسيطة (Suspense Accounts)
    Checklist->>DB: فحص مطابقة الأستاذ المساعد مع العام (Subledger Variances)
    Checklist-->>CloseService: تقرير الفحص (BlockersCount, WarningsCount)
    deactivate Checklist

    alt وجود مانع واحد أو أكثر (BlockersCount > 0)
        CloseService-->>CFO: رفض الإقفال مع بيان الموانع: ErrClosingPreCheckFailed
    end

    %% 2. إعادة تقييم العملات الأجنبية (IAS 21 FX Revaluation)
    CloseService->>FXEngine: RunYearEndFXRevaluation(companyID, closingDate)
    activate FXEngine
    FXEngine->>DB: جلب أرصدة الحسابات النقدية بالعملات الأجنبية
    FXEngine->>DB: جلب أسعار الإقفال الفورية (Closing Spot Rates)
    FXEngine->>GL: توليد وترحيل قيد فروق أسعار الصرف غير المحققة (Unrealized FX Move)
    FXEngine-->>CloseService: تم ترحيل قيود تقييم العملة
    deactivate FXEngine

    %% 3. توليد قيد الإقفال السنوي في فترة التسوية 13 (Closing Move)
    CloseService->>GL: إنشاء قيد الإقفال السنوي (TargetPeriodNumber = 13)
    activate GL
    Note over GL: تصفير كافة حسابات الإيرادات والمصروفات<br/>ترحيل صافي الدخل (صافي الربح/الخسارة)<br/>إلى حساب الأرباح المبقاة (Retained Earnings)
    GL->>DB: ترحيل القيد وتثبيته: PostEntry (Classification = 'closing')
    GL-->>CloseService: تم إنشاء القيد: ClosingEntryID
    deactivate GL

    %% 4. تجميد السنة والفترات نهائياً (Hard Closing)
    CloseService->>DB: تحديث حالة السنة والفترة 13: SET state = 'hard_closed'
    Note over DB: تفعيل الأقفال التشاؤمية ومنع أي ترحيل لاحق بتاريخ السنة

    %% 5. توليد قيد الافتتاح للسنة الجديدة (Opening Move)
    opt توليد قيد الافتتاح تلقائيًا للسنة التالية
        CloseService->>GL: توليد قيد الافتتاح (TargetPeriodNumber = 1 / Classification = 'opening')
        Note over GL: تدوير أرصدة الميزانية العمومية فقط (أصول، خصوم، حقوق ملكية)<br/>بدء حسابات الدخل برصيد صفري تام
        GL->>DB: ترحيل قيد الافتتاح
    end

    CloseService-->>CFO: نجاح الإقفال السنوي وتوليد التقارير المالية المعتمدة
    deactivate CloseService
```

---

## 2. عناصر قائمة التحقق الإلزامية (Pre-Close Checklist Invariants)

| الفحص | النوع | شرط النجاح | الإجراء عند الرسوب |
| --- | --- | --- | --- |
| **القيود المسودة (Drafts)** | **مانع حاسم (Blocker)** | عدم وجود أي قيد بحالة `draft` في السنة | منع الإقفال حتى ترحيل أو حذف المسودات. |
| **تتابع إقفال الفترات (1-12)** | **مانع حاسم (Blocker)** | جميع الفترات من 1 إلى 12 بحالة `hard_closed` | منع الإقفال حتى استكمال إقفال الفترات شهريًا. |
| **فترة التسوية (Period 13)** | **مانع حاسم (Blocker)** | وجود فترة تسوية نشطة مخصصة لقيد الإقفال | إلزام النظام بإنشاء الفترة 13 قبل الترحيل. |
| **الحسابات الوسيطة (Suspense)** | **مانع حاسم (Blocker)** | رصيد حساب التسوية الوسيط يساوي صفرًا تمامًا | منع الإقفال لوجود عمليات معلقة غير معالجة. |
| **فروقات الأستاذ المساعد** | **مانع حاسم (Blocker)** | مطابقة الأستاذ المساعد مع حساب المراقبة بنسبة 100% | منع الإقفال لحين تصحيح الفروقات المعلقة. |
| **تقييم العملات الأجنبية** | **تحذير (Warning)** | تشغيل إعادة التقييم لآخر يوم في السنة المالية | تنبيه المسؤول المالي لتطبيق معيار IAS 21. |
