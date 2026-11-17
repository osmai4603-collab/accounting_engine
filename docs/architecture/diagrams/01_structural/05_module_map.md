# خريطة الوحدات المعيارية والدمج (Module Map — IFRS / IAS & Consolidation)

- **النطاق (Scope):** موقع معايير المحاسبة الدولية (IFRS 9, 15, 16, IAS 12, IAS 21) ومحرك القوائم الموحدة (Consolidation)، وكيفية اتصالها بمحرك الترحيل والتقارير.
- **مصدر الحقيقة (Source of Truth):** `modules/*`, `internal/ifrs*`, `internal/ias12`, `internal/consolidation`, `internal/valuation`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري

```mermaid
flowchart TB
    classDef standard fill:#fdf2f8,stroke:#be185d,stroke-width:2px,color:#0f172a;
    classDef glue fill:#eff6ff,stroke:#1d4ed8,stroke-width:2px,color:#0f172a;
    classDef core fill:#fffbeb,stroke:#b45309,stroke-width:2px,color:#0f172a;
    classDef reporting fill:#ecfdf5,stroke:#047857,stroke-width:2px,color:#0f172a;

    subgraph StandardsEngines ["وحدات المعايير المحاسبية الدولية (IFRS / IAS Standards Modules)"]
        IFRS9["<b>IFRS 9</b>: الأدوات المالية<br/>خسائر الائتمان المتوقعة (ECL)<br/>تصنيف الأصول والمطلوبات"]:::standard
        IFRS15["<b>IFRS 15</b>: الإيرادات من العقود<br/>التزامات الأداء (POB)<br/>توزيع سعر المعاملة"]:::standard
        IFRS16["<b>IFRS 16</b>: عقود الإيجار<br/>أصل حق الاستخدام (ROU)<br/>التزام عقد الإيجار والإطفاء"]:::standard
        IAS12["<b>IAS 12</b>: ضرائب الدخل<br/>الفروق المؤقتة<br/>الضريبة المؤجلة (DTA / DTL)"]:::standard
        IAS21["<b>IAS 21</b>: آثار تغير أسعار الصرف<br/>العملة الوظيفية وعملة العرض<br/>أرباح/خسائر الصرف غير المحققة"]:::standard
    end

    subgraph CoordinationAndAutomation ["طبقة التنسيق والأتمتة (Automation & SPI Bridge)"]
        Automation["محرك أتمتة المعايير<br/><code>internal/ifrsautomation</code>"]:::glue
        SPI_Standards["منفذ المعايير الموحد<br/><code>pkg/spi/standards</code><br/>(StandardUnit & EngineAdapter)"]:::glue
        Valuation["محرك تقييم العملات والأسعار<br/><code>internal/valuation</code>"]:::glue
    end

    subgraph CoreLedger ["نواة الترحيل المحاسبي (General Ledger Core)"]
        GL_Posting["محرك الترحيل المركزي<br/><code>internal/posting.Engine</code><br/>(قيد متوازن، تتابع، أقفال)"]:::core
        JournalEntries[("دفتر اليومية الموحد<br/>account_moves & lines")]:::core
    end

    subgraph OutputAndConsolidation ["القوائم المالية والدمج المالي (Reporting & Consolidation)"]
        GL_Reporting["محرك التقارير المالية الفردية<br/><code>internal/reporting</code><br/>(TB, P&L, BS, Cash Flow)"]:::reporting
        Consolidation["محرك القوائم المالية الموحدة<br/><code>internal/consolidation</code><br/>(استبعاد المعاملات البينية Elimination)"]:::reporting
        ConsolidatedFS["حزمة التقارير الموحدة للمجموعة<br/>Consolidated Financial Bundle"]:::reporting
    end

    %% التدفق من المعايير إلى الترحيل
    IFRS9 -->|أحداث مخصصات ECL| SPI_Standards
    IFRS15 -->|قيود الاعتراف بالإيراد| SPI_Standards
    IFRS16 -->|قيود إثبات الالتزام والإهلاك| SPI_Standards
    IAS12 -->|قيود تسوية الضريبة| SPI_Standards
    IAS21 --> Valuation

    Valuation -->|فروق تقييم العملة| Automation
    SPI_Standards --> Automation
    Automation -->|توليد قيود محاسبية متوازنة| GL_Posting

    GL_Posting --> JournalEntries
    JournalEntries --> GL_Reporting

    %% تدفق الدمج
    GL_Reporting --> Consolidation
    Consolidation -->|تطبيق جدول الحصص والاستبعاد| ConsolidatedFS
```

---

## 2. مصفوفة تكامل المعايير الدولية

| المعيار المحاسبي | الحزمة في الكود | المدخلات الأساسية | نوع القيد المولد في الدفتر العام | حسابات الأستاذ العام المتأثرة |
| --- | --- | --- | --- | --- |
| **IFRS 9** | `internal/ifrs9`, `modules/ifrs9` | أعمار الديون، مراحل التعثر (Stage 1/2/3) | قيد تسوية مخصص (`adjusting`) | من حـ/ مصروف مخصص خسائر الائتمان إلى حـ/ مخصص التعثر المتراكم |
| **IFRS 15** | `internal/ifrs15`, `modules/ifrs15` | بنود عقود العملاء، إنجاز التزامات الأداء | قيد إيراد وإثبات عقد (`regular`) | من حـ/ أصول العقود إلى حـ/ التزامات العقود وحـ/ الإيراد المحقق |
| **IFRS 16** | `internal/ifrs16`, `modules/ifrs16` | جدول دفعات الإيجار، سعر الخصم الفعلي | قيد أصل والتزام وإهلاك شهري | حـ/ أصل حق الاستخدام (ROU)، حـ/ التزام الإيجار، حـ/ مصروف الفائدة |
| **IAS 12** | `internal/ias12`, `modules/ias12` | الفروق بين الأساس المحاسبي والأساس الضريبي | قيد ضريبة مؤجلة نهاية الفترة | حـ/ أصول ضريبية مؤجلة (DTA) أو حـ/ التزامات ضريبية مؤجلة (DTL) |
| **IAS 21** | `internal/valuation`, `reporting/currency` | أسعار الإقفال الفورية (Closing Spot Rates) | قيد إعادة تقييم بنود نقدية (`adjusting`) | حـ/ أرباح أو خسائر فروق أسعار الصرف غير المحققة (FX Gain/Loss) |
| **Consolidation** | `internal/consolidation`, `reporting/consolidated` | موازين مراجعة الشركات التابعة ونسب التملك | قيود استبعاد دفترية (Elimination Entries) | استبعاد الاستثمار مقابل حقوق الملكية، وإلغاء المبيعات والذمم البينية |
