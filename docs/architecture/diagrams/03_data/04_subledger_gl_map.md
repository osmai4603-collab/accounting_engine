# خريطة مطابقة الدفاتر المساعدة مع الأستاذ العام (Subledger → GL Control Account Map)

- **النطاق (Scope):** العلاقة بين الدفاتر المساعدة (AR, AP, Fixed Assets, Treasury) وحسابات المراقبة المقابلة في الدفتر العام، ومسار المطابقة الآلية وإصدار الشهادات عبر `gl_reconciliation.go`.
- **مصدر الحقيقة (Source of Truth):** `internal/subledgers/gl_reconciliation.go`, `internal/subledgers/control_accounts.go`, `docs/architecture/subledgers_architecture.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: خريطة الدفاتر وحسابات المراقبة

```mermaid
flowchart LR
    classDef subledger fill:#eff6ff,stroke:#1d4ed8,stroke-width:2px,color:#0f172a;
    classDef control fill:#fef3c7,stroke:#d97706,stroke-width:2px,color:#0f172a;
    classDef recon fill:#ecfdf5,stroke:#047857,stroke-width:2px,color:#0f172a;
    classDef db fill:#f8fafc,stroke:#475569,stroke-width:2px,color:#0f172a;

    subgraph Subledgers ["1. طبقة الدفاتر المساعدة (Subledgers)"]
        AR["<b>حسابات العملاء (AR)</b><br/>فواتير المبيعات، المقبوضات،<br/>والأرصدة المفتوحة لكل شريك"]:::subledger
        AP["<b>حسابات الموردين (AP)</b><br/>فواتير المشتريات، المدفوعات،<br/>والتزامات الشركاء"]:::subledger
        FA["<b>الأصول الثابتة (Fixed Assets)</b><br/>تكلفة الأصول الرأسمالية<br/>ومجمعات الإهلاك المتراكمة"]:::subledger
        TR["<b>الخزينة والمصارف (Treasury)</b><br/>كشوف الحسابات المصرفية<br/>والصناديق النقدية النثرية"]:::subledger
    end

    subgraph ControlAccounts ["2. حسابات المراقبة في الدفتر العام (GL Control Accounts)"]
        GL_AR["حـ/ مراقبة المدينين والعملاء<br/>(Trade Receivables Control)"]:::control
        GL_AP["حـ/ مراقبة الدائنين والموردين<br/>(Trade Payables Control)"]:::control
        GL_FA_Cost["حـ/ مراقبة الأصول الثابتة بالتكلفة<br/>(Fixed Assets at Cost)"]:::control
        GL_FA_Depr["حـ/ مراقبة مجمع إهلاك الأصول<br/>(Accumulated Depreciation)"]:::control
        GL_Bank["حـ/ مراقبة الحسابات المصرفية<br/>(Bank & Cash Accounts)"]:::control
    end

    subgraph ReconciliationEngine ["3. محرك المطابقة والشهادات (gl_reconciliation.go)"]
        ReconService["<b>خدمة المطابقة الدورية</b><br/><code>ReconciliationService</code>"]:::recon
        DiscrepancyCheck{"فحص الفروقات<br/>Subledger == GL؟"}:::recon
        DiscrepancyReport["تقرير فروقات مفصل<br/><code>DiscrepancyReport</code><br/>(Domain, ControlAccount, Difference)"]:::recon
        Certificate["شهادة مطابقة رسمية معتمدة<br/><code>ReconciliationCertificate</code><br/>(Clean = true / false)"]:::recon
    end

    %% تغذية الدفاتر لحسابات المراقبة
    AR -->|توليد قيود بالعملة الوظيفية| GL_AR
    AP -->|توليد قيود بالعملة الوظيفية| GL_AP
    FA -->|قيود الرسملة والإضافات| GL_FA_Cost
    FA -->|قيود الإهلاك الدورية| GL_FA_Depr
    TR -->|قيود المقبوضات والمدفوعات| GL_Bank

    %% مسار المطابقة الدورية
    AR -.->|جلب مجموع الأرصدة الفرعية| ReconService
    AP -.->|جلب مجموع الأرصدة الفرعية| ReconService
    FA -.->|جلب القيمة الدفترية للأصول| ReconService
    TR -.->|جلب أرصدة الكشوف المصرفية| ReconService

    GL_AR -.->|قراءة رصيد الأستاذ العام| ReconService
    GL_AP -.->|قراءة رصيد الأستاذ العام| ReconService
    GL_FA_Cost -.->|قراءة رصيد الأستاذ العام| ReconService
    GL_Bank -.->|قراءة رصيد الأستاذ العام| ReconService

    ReconService --> DiscrepancyCheck
    DiscrepancyCheck -- يوجد فرق != 0 --> DiscrepancyReport
    DiscrepancyCheck -- الفرق صفري 100% --> Certificate
```

---

## 2. معادلة المطابقة الحاكمة (Reconciliation Invariant)

في أي تاريخ تقرير مالي، يجب أن يتحقق شرط التطابق التالي قطعيًا:

$$\sum_{i=1}^{N} \text{Subledger Open Balance}_i = \text{Balance of GL Control Account}$$

### إجراءات التعامل مع الفروقات (Discrepancy Resolution)

1. **في حال وجود فرق (`HasVariance() == true`):**
   - تُسجل الواقعة فورًا في جدول `subledger_reconciliation_logs`.
   - تُدرج الفروقات في `subledger_reconciliation_exceptions`.
   - **تُمنع عملية إقفال الفترة المالية** من خلال قائمة التحقق الآلية (`Checklist Blocker`) حتى تسوية الفارق.
2. **في حال التطابق التام (`Clean == true`):**
   - يُصدر المحرك `ReconciliationCertificate` موثقة برقم وتوقيع زمني وتُربط بحزمة إقفال الفترة.
