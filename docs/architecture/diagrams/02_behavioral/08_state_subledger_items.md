# آلة حالة بنود الأستاذ المساعد (State Machine: Fixed Assets & AR/AP Open Items)

- **النطاق (Scope):** دورة حياة بطاقة الأصل الثابت (Fixed Asset Lifecycle) ودورة حياة البنود المفتوحة للعملاء والموردين (AR/AP Open Item Lifecycle).
- **مصدر الحقيقة (Source of Truth):** `internal/subledgers/fixed_assets.go`, `internal/subledgers/public_arap.go`, `internal/subledgers/receivable.go`, `docs/architecture/subledgers_architecture.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. دورة حياة الأصل الثابت (Fixed Asset Lifecycle — IAS 16)

```mermaid
stateDiagram-v2
    [*] --> Draft : تسجيل أصل جديد (CreateAsset)

    state Draft {
        [*] --> Registered
        Registered : تكلفة الشراء، تاريخ البدء، طريقة الإهلاك، العمر الإنتاجي
        Registered : لم يُرحل له قيد رأسمالي بعد
    }

    Draft --> Active : رسملة الأصل وبدء التشغيل (Capitalize / Activate)<br/>[توليد قيد في الدفتر العام: من حـ/ الأصول الثابتة إلى حـ/ المشروعات تحت التنفيذ أو المورد]

    state Active {
        [*] --> Depreciating
        Depreciating : تشغيل الإهلاك الدوري (RunDepreciation)<br/>توليد قيد شهري: من حـ/ مصروف الإهلاك إلى حـ/ مجمع الإهلاك
        Depreciating : إعادة تقييم الأصل عند الحاجة (Revaluation)
        Depreciating : انخفاض القيمة إن وجد (Impairment - IAS 36)
    }

    Active --> FullyDepreciated : وصول القيمة الدفترية إلى قيمة الخردة (Book Value == Salvage Value)
    
    state FullyDepreciated {
        [*] --> ZeroNetBookValue
        ZeroNetBookValue : أصل مستهلك دفترياً بالكامل وما زال يعمل بالمنشأة
        ZeroNetBookValue : يتوقف توليد قيود الإهلاك تلقائياً
    }

    Active --> Disposed : استبعاد، بيع، أو تخريد الأصل (Dispose / Sell)
    FullyDepreciated --> Disposed : استبعاد الأصل المستهلك بالكامل

    state Disposed {
        [*] --> Terminated
        Terminated : توليد قيد التخلص النهائي: إقفال مجمع الإهلاك والأصل<br/>وإثبات أرباح/خسائر استبعاد الأصول الثابتة
    }

    Disposed --> [*] : إغلاق بطاقة الأصل نهائيًا
```

---

## 2. دورة حياة البنود المفتوحة للذمم المدينة والدائنة (AR/AP Open Item Lifecycle)

```mermaid
stateDiagram-v2
    [*] --> Open : ترحيل فاتورة مبيعات/شراء مع مرفق بند مفتوح (OpenLineAttachment)

    state Open {
        [*] --> Unpaid
        Unpaid : الرصيد المتبقي = المبلغ الأصلي بالكامل
        Unpaid : يظهر في سجل أعمار الديون (Aging Schedule)
        Unpaid : مؤهل للمطابقة مع المدفوعات أو السندات
    }

    Open --> PartiallyReconciled : سداد جزئي (Partial Payment / Allocation)<br/>[توليد قيد قبض/صرف مع تخصيص جزء من المبلغ]

    state PartiallyReconciled {
        [*] --> RemainingBalance
        RemainingBalance : 0 < ReconciledAmount < TotalAmount
        RemainingBalance : الرصيد المتبقي يخضع لتقييم فروق الصرف (FX Revaluation)
    }

    PartiallyReconciled --> PartiallyReconciled : دفعات جزئية إضافية
    PartiallyReconciled --> FullyReconciled : سداد المبلغ المتبقي بالكامل

    Open --> FullyReconciled : سداد كامل الفاتورة دفعة واحدة (Full Settlement)

    state FullyReconciled {
        [*] --> ClosedItem
        ClosedItem : ReconciledAmount == TotalAmount
        ClosedItem : إغلاق البند وتسويته في جدول subledger_open_lines
        ClosedItem : لا يظهر في أعمار الديون المفتوحة
    }

    Open --> WrittenOff : إعدام الدين أو شطب الرصيد (Write-Off / Bad Debt)
    PartiallyReconciled --> WrittenOff : شطب الرصيد المتبقي المتعثر

    state WrittenOff {
        [*] --> BadDebtRecognized
        BadDebtRecognized : توليد قيد شطب ديون معدومة بالدفتر العام
        BadDebtRecognized : إقفال المطابقة نهائيًا
    }

    FullyReconciled --> [*]
    WrittenOff --> [*]
```

