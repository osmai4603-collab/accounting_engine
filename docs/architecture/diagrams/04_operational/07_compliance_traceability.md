# مخطط تتبع الامتثال للمعايير الدولية (Compliance Traceability Architecture)

- **النطاق (Scope):** شجرة تتبع متطلبات معايير المحاسبة الدولية (IFRS / IAS / AAOIFI) من نصوص المعيار إلى الوحدات البرمجية المنفذة، والاختبارات التثبيتية، والأدلة المستندية.
- **مصدر الحقيقة (Source of Truth):** `docs/ENGINE_COMPLIANCE_MATRIX.md`, `modules/*`, `internal/ifrs*`, `test/standards`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: سلسلة تتبع الامتثال (Compliance Traceability)

```mermaid
flowchart LR
    classDef standard fill:#fdf2f8,stroke:#be185d,stroke-width:2px,color:#0f172a;
    classDef module fill:#eff6ff,stroke:#1d4ed8,stroke-width:2px,color:#0f172a;
    classDef test fill:#ecfdf5,stroke:#047857,stroke-width:2px,color:#0f172a;
    classDef evidence fill:#fefce8,stroke:#ca8a04,stroke-width:2px,color:#0f172a;

    subgraph IFRS_Standards ["1. نصوص المعايير المحاسبية الدولية (IFRS / IAS)"]
        S_IFRS9["<b>IFRS 9</b>: خسائر الائتمان المتوقعة<br/>(Expected Credit Loss - ECL)<br/>نماذج المراحل 1 و 2 و 3"]:::standard
        S_IFRS15["<b>IFRS 15</b>: الإيرادات من العقود<br/>التزامات الأداء المنفصلة (POBs)<br/>والاعتراف على مدى الوقت"]:::standard
        S_IFRS16["<b>IFRS 16</b>: عقود الإيجار<br/>أصل حق الاستخدام والتزام الإيجار<br/>وجدول الفوائد والإهلاك"]:::standard
        S_IAS12["<b>IAS 12</b>: ضرائب الدخل<br/>الفروق المؤقتة الخاضعة للضريبة<br/>والأصول والالتزامات المؤجلة"]:::standard
        S_IAS21["<b>IAS 21</b>: آثار أسعار الصرف<br/>العملة الوظيفية والترجمة<br/>وفروق الصرف غير المحققة"]:::standard
        S_IAS1["<b>IAS 1</b>: عرض القوائم المالية<br/>التوازن التام وعدم المقاصة غير القانونية<br/>ميزان المراجعة والقوائم الأربع"]:::standard
    end

    subgraph CodeUnits ["2. الوحدات البرمجية المنفذة (Engine Code Units)"]
        U_IFRS9["<code>internal/ifrs9</code><br/><code>modules/ifrs9</code>"]:::module
        U_IFRS15["<code>internal/ifrs15</code><br/><code>modules/ifrs15</code>"]:::module
        U_IFRS16["<code>internal/ifrs16</code><br/><code>modules/ifrs16</code>"]:::module
        U_IAS12["<code>internal/ias12</code><br/><code>modules/ias12</code>"]:::module
        U_IAS21["<code>internal/valuation</code><br/><code>reporting/currency</code>"]:::module
        U_IAS1["<code>internal/posting</code><br/><code>internal/reporting</code>"]:::module
    end

    subgraph TestVerification ["3. حزم الاختبارات المؤكدة (Automated Test Suites)"]
        T_IFRS9["<code>test/standards/ifrs9_test.go</code><br/>اختبار تحول المراحل واحتساب ECL"]:::test
        T_IFRS15["<code>test/standards/ifrs15_test.go</code><br/>اختبار عقود متعددة الالتزامات"]:::test
        T_IFRS16["<code>test/standards/ifrs16_test.go</code><br/>اختبار مطابقة جدول الدفعات والإهلاك"]:::test
        T_IAS12["<code>test/standards/ias12_test.go</code><br/>اختبار الفروق المؤقتة ومعدل الضريبة"]:::test
        T_IAS21["<code>test/standards/ias21_test.go</code><br/>اختبار إعادة تقييم البنود النقدية"]:::test
        T_IAS1["<code>test/standards/financial_statements_test.go</code><br/>اختبار اتساق القوائم والتوازن"]:::test
    end

    subgraph ComplianceEvidence ["4. الأدلة والتقارير المستندية (Auditor Evidence)"]
        E_IFRS9["مصفوفة مخصصات الديون المعتمدة<br/>ECL Staging Report"]:::evidence
        E_IFRS15["سجل العقود والتزامات الأداء<br/>Revenue Recognition Schedule"]:::evidence
        E_IFRS16["سجل التزامات وأصول الإيجار<br/>Lease Liability Amortization Schedule"]:::evidence
        E_IAS12["جدول تسوية الضريبة المؤجلة<br/>Deferred Tax Reconciliation"]:::evidence
        E_IAS21["تقرير فروق تقييم العملات المحققة وغير المحققة<br/>FX Gain/Loss Ledger"]:::evidence
        E_IAS1["حزمة القوائم المالية الكاملة الموقعة<br/>Certified Financial Statements Bundle"]:::evidence
    end

    S_IFRS9 --> U_IFRS9 --> T_IFRS9 --> E_IFRS9
    S_IFRS15 --> U_IFRS15 --> T_IFRS15 --> E_IFRS15
    S_IFRS16 --> U_IFRS16 --> T_IFRS16 --> E_IFRS16
    S_IAS12 --> U_IAS12 --> T_IAS12 --> E_IAS12
    S_IAS21 --> U_IAS21 --> T_IAS21 --> E_IAS21
    S_IAS1 --> U_IAS1 --> T_IAS1 --> E_IAS1
```

---

## 2. جدول إثبات الامتثال للمراجع القانوني والمدقق الخارجي

| المعيار المحاسبي الدولي | المتطلب الإلزامي | التنفيذ في الكود | وسيلة الفحص الآلي المستمر |
| --- | --- | --- | --- |
| **IFRS 9 §5.5** | احتساب مخصص خسائر الائتمان على أساس التطلع المستقبلي (Forward-looking). | حساب احتمالية التعثر (PD) والخسارة عند التعثر (LGD) في `internal/ifrs9`. | اختبار مصفوفة انتقال المراحل الثلاث في `test/standards`. |
| **IFRS 15 §31** | الاعتراف بالإيراد عند الوفاء بالتزام الأداء (Satisfaction of Performance Obligation). | توزيع سعر المعاملة عبر `Relative Standalone Selling Price` في `internal/ifrs15`. | فحص سيناريوهات العقود المجمعة في `ifrs15_test.go`. |
| **IFRS 16 §22** | إثبات أصل حق الاستخدام والتزام عقد الإيجار عند تاريخ بدء العقد. | احتساب القيمة الحالية لدفعات الإيجار بمعدل الفائدة الضمني في `internal/ifrs16`. | مطابقة جداول الإطفاء والإهلاك شهريًا. |
| **IAS 12 §15** | إثبات التزام ضريبي مؤجل لجميع الفروق المؤقتة الخاضعة للضريبة. | حساب الفرق بين الأساس الدفتري والضريبي وضربه في معدل الضريبة المعمول به في `internal/ias12`. | التحقق من ترحيل قيود التسوية الضريبية. |
| **IAS 21 §23** | ترجمة البنود النقدية بالعملات الأجنبية باستخدام سعر الإقفال الفوري في نهاية كل فترة. | فرز البنود النقدية وإعادة تقييمها وتوليد قيود أرباح/خسائر تقييم في `internal/valuation`. | فحص سيناريوهات فروق أسعار الصرف غير المحققة. |
| **IAS 1 §15** | العرض العادل والامتثال التام لمعايير التقارير المالية الدولية وعدم المقاصة. | التحقق من صحة القوائم وعدم دمج المدين مع الدائن إلا بحكم معيار صريح في `internal/reporting`. | اختبار إعادة إنتاج القوائم التاريخية بدقة متطابقة. |
