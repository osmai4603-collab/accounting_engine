# مخطط بوابات الجودة والتكامل المستمر (CI/CD Quality Gates & Test Map)

- **النطاق (Scope):** ربط كل بوابة فحص في مسار CI بالاختبارات المتخصصة والخاصية المعمارية/المحاسبية التي تحميها لمنع التدهور البرمجي (Regressions).
- **مصدر الحقيقة (Source of Truth):** `docs/ci.md`, `test/acid`, `test/concurrency`, `test/contract_parity`, `test/standards`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: بوابات الفحص الصارمة (Quality Gates Pipeline)

```mermaid
flowchart TD
    classDef gate fill:#eff6ff,stroke:#2563eb,stroke-width:2px,color:#0f172a;
    classDef test fill:#f8fafc,stroke:#334155,stroke-width:2px,color:#0f172a;
    classDef prop fill:#ecfdf5,stroke:#059669,stroke-width:2px,color:#0f172a;
    classDef block fill:#fee2e2,stroke:#dc2626,stroke-width:2px,color:#7f1d1d;

    Push["دفع كود أو فتح PR جديد"] --> GateLint["بوابة الفحص الثابت والتنسيق<br/>(Static Analysis & Formatting)"]:::gate

    GateLint --> TestLint["golangci-lint + go vet"]:::test
    TestLint --> Prop1["نظافة الكود ومنع تسرب الذاكرة والمؤشرات"]:::prop

    GateLint --> GatePurity["بوابة نقاء العقود العامة<br/>(Public API Purity Gate)"]:::gate
    GatePurity --> TestPurity["scripts/check_public_api_purity.sh<br/>scripts/apisnapshot/main.go"]:::test
    TestPurity --> Prop2["منع التعديل غير المعتمد في pkg/api<br/>ومنع تسرب الأنواع الداخلية"]:::prop

    GatePurity --> GateUnit["بوابة المنطق المحاسبي الذري<br/>(Unit & Invariants Gate)"]:::gate
    GateUnit --> TestUnit["internal/posting/*_test.go<br/>internal/masterdata/*_test.go"]:::test
    TestUnit --> Prop3["فحص التوازن، تتابع الفترات،<br/>تقريب العملات، والفصل الرقابي"]:::prop

    GateUnit --> GateACID["بوابة معاملات قواعد البيانات<br/>(ACID & PostgreSQL Gate)"]:::gate
    GateACID --> TestACID["test/acid/*_test.go<br/>test/atomic/*_test.go"]:::test
    TestACID --> Prop4["ضمان الـ Rollback عند الفشل،<br/>وحرمة القيود، وتفعيل Triggers"]:::prop

    GateACID --> GateConcurrency["بوابة السباق والتزامن العالي<br/>(Concurrency & Race Gate)"]:::gate
    GateConcurrency --> TestRace["test/concurrency/*_test.go<br/>go test -race ./..."]:::test
    TestRace --> Prop5["منع تصادم أرقام اليومية،<br/>وتفادي الـ Deadlocks تحت 100+ خيط متزامن"]:::prop

    GateConcurrency --> GateParity["بوابة التطابق والتوافق<br/>(Contract Parity Gate)"]:::gate
    GateParity --> TestParity["test/contract_parity/*_test.go"]:::test
    TestParity --> Prop6["تطابق سلوك محول الذاكرة ومحول PostgreSQL 100%<br/>في كافة حالات الاستخدام"]:::prop

    GateParity --> GateStandards["بوابة المعايير الدولية<br/>(IFRS / IAS Standards Gate)"]:::gate
    GateStandards --> TestStandards["test/standards/*_test.go<br/>test/service_integration"]:::test
    TestStandards --> Prop7["صحة حسابات IFRS 9/15/16 و IAS 12/21<br/>وتطابق التقارير المالية"]:::prop

    Prop7 --> DeployReady(["اجتياز كافة البوابات بنجاح<br/>جاهز للنشر المعتمد"]):::block
```

---

## 2. جدول ربط الفحوصات بالخصائص المحمية (Quality Gates Map)

| اسم الفحص / المسار | الأمر المنفذ في CI | الخاصية المحمية (Guaranteed Property) | الأثر عند الفشل |
| --- | --- | --- | --- |
| **API Purity Gate** | `scripts/check_public_api_purity.sh` | استقرار وحرمة العقود العامة ومنع كسر توافق واجهات التكامل الخارجية (`SemVer`). | إيقاف البناء فورًا لمنع كسر عملاء المكتبة. |
| **ACID Integrity** | `go test -v ./test/acid/...` | الذرية التامة (`All-or-Nothing`) وعدم بقاء أي أثر جزئي عند فشل الترحيل. | منع نشر أي تعديل يكسر تراجع المعاملات. |
| **Race & Deadlock** | `go test -race ./test/concurrency/...` | الترتيب الحتمي للأقفال وسلامة تخصيص التسلسل تحت الضغط المتزامن العالي. | منع نشر أي كود يسبب تعليق العمليات. |
| **Contract Parity** | `go test -v ./test/contract_parity/...` | تطابق سلوك `storage/memory` مع `storage/postgres` لضمان صحة الاختبارات. | رفض المحولات غير الملتزمة بالعقود. |
| **RLS Penetration** | `go test -v ./internal/adapters/postgres -run TestRLS` | استحالة قراءة أو كتابة بيانات شركة أخرى حتى مع محاولات التزوير الخبيثة. | خرق أمني يمنع النشر قطعيًا. |
| **Standards Math** | `go test -v ./test/standards/...` | صحة المعادلات المحاسبية لـ IFRS و IAS وتطابق أرقام القوائم المالية. | منع الأخطاء المحاسبية في التقارير. |
