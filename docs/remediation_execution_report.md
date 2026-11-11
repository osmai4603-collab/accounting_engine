# تقرير إغلاق خطة المعالجة والتحديث

## Accounting Engine Remediation Closure Report

**تاريخ الإغلاق:** نوفمبر 2026  
**الخطة المرجعية:** [`docs/remediation_execution_plan.md`](./remediation_execution_plan.md)  
**نطاق التحقق:** المراحل الست كاملة، مع تركيز خاص على المراحل 2-6.

---

## 1. ملخص الحالة النهائية

| المرحلة | النطاق | الحالة | الدليل الرئيسي |
| ------- | ------ | ------ | -------------- |
| **1** | سلامة النواة المحاسبية وقاعدة البيانات | ✅ مكتملة | `TestMultiCurrencyTriggerPostingBalance`، `TestConcurrentSequenceAllocation`، `TestBulkAtomicity`، `TestRoundingDifferenceHandling` |
| **2** | الحوكمة والموافقات وسجل التدقيق الذري | ✅ مكتملة | `TestApprovalAmountTamperingRejection`، `TestMakerCheckerThresholdEnforcement`، `TestAtomicPostingAuditLogCreation` |
| **3** | سلامة وذرية الدفاتر المساعدة | ✅ مكتملة | `TestEventBridgeRejectsIdempotencyPayloadConflicts`، `TestConcurrentDisposalDraftsReserveAssetOnce`، `TestPhase3_AtomicAssetDisposalAndImpairmentPostgres` |
| **4** | البيانات الأساسية والتقارير متعددة الشركات | ✅ مكتملة | `TestUpdateAccountParentPostgresCompanyScope`، `TestReportFacadeWithCompanyContext`، `TestBalanceReconciliationFacadePassesCompanyScope` |
| **5** | نضج معايير IFRS والأتمتة | ✅ مكتملة | `TestIrregularPeriodLeaseDiscounting`، `TestCLIAutomationExecutionWithJobs` |
| **6** | المعمارية و ISP والجاهزية الإنتاجية | ✅ مكتملة | `TestNewWithPortsAcceptsIndependentStoragePorts`، `TestNewWithPortsRequiresEveryPort`، `TestBaselineMigrationCleanRun` |

**النتيجة:** تحققت جميع معايير القبول والإغلاق الأربعة في القسم 3 من الخطة.

---

## 2. تفاصيل التنفيذ

### المرحلة 1: سلامة النواة المحاسبية وقاعدة البيانات

- إصلاح Trigger توازن القيود ليستخدم `functional_debit_minor` و`functional_credit_minor` عبر الترحيل `202611110001_fix_balance_trigger.sql`.
- إصلاح `PostBulk` والتسلسل المحاسبي عبر `202611110002_bulk_posting_triggers.sql`، مع حجز نطاق الأرقام التسلسلية داخل المعاملة.
- توحيد عقد الدقة والتقريب واختبار فروق التقريب.
- تقوية Triggers الترحيل عبر `202611110003_robust_posting_triggers.sql`.

### المرحلة 2: الحوكمة والرقابة وسجل التدقيق

- ربط طلب الموافقة ببصمة محتوى القيد (`EntryContentHash`) وبالمبلغ الوظيفي (`EntryFunctionalTotal`) في [`internal/governance/approval.go`](../internal/governance/approval.go) و[`internal/posting/content_hash.go`](../internal/posting/content_hash.go).
- إضافة `ErrApprovalAmountMismatch` و`ErrApprovalAlreadyExists`، ومنع إعادة استخدام الموافقة لقيد مختلف أو مبلغ مختلف.
- الترحيل `202611110004_phase2_approval_binding.sql`: أعمدة `entry_hash` و`functional_amount` و`functional_currency`، وفهرس فريد `uq_approval_requests_company_entry`، وجدول `governance_policies`.
- تفعيل بوابة الموافقات `ApprovalGate` على حد الترحيل، مع تطبيق سياسة maker-checker حسب شركة القيد.
- دمج سجل التدقيق في المعاملة نفسها: `POST` و`REVERSE` و`FISCAL_YEAR_CLOSED` و`ACCOUNT_CREATED` و`ACCOUNT_UPDATED` عبر `appendEntryAuditTx` و`appendEntryAuditBatchTx` و`appendEntityAuditTx`.
- الترحيل `202611110005_audit_payload_bytes.sql`: تحويل `payload_before/after` إلى `bytea` لتمكين التحقق من سلسلة التجزئة، مع اقتطاع الوقت إلى microseconds لمطابقة `timestamptz`.
- الحفاظ على أداء `PostBulk` فوق 2000 قيد/ثانية باستخدام قفل استشاري واحد و`pgx.Batch` لسجل التدقيق.

### المرحلة 3: سلامة وذرية الدفاتر المساعدة

- إضافة `EconomicEventPayloadHash` و`ErrIdempotencyPayloadConflict` في [`internal/subledgers/event_hash.go`](../internal/subledgers/event_hash.go).
- `EventBridge` يعيد نفس القيد عند إعادة الإرسال المتطابق، ويرفض إعادة استخدام مفتاح idempotency بحمولة مختلفة، مع عزل النطاق حسب الشركة.
- إضافة `PendingOperation` و`PendingOperationEntryID` للأصول الثابتة، مع مراجع استبعاد حتمية.
- جعل الاستبعاد والتدني ذريين: لا يتغير الأصل إلا بعد ترحيل القيد وتأكيده، مع منع الاستبعاد المزدوج تحت التزامن.
- إضافة واجهات المستودع الذرية ودوال `*Context`، وإتاحة `ForCompany` لخدمات الدفاتر المساعدة.

### المرحلة 4: البيانات الأساسية والتقارير المالية

- إضافة `UpdateAccountParentContext(ctx, account, parentID)` في [`internal/masterdata/service.go`](../internal/masterdata/service.go)، مع قراءة الحسابات الأب داخل نطاق الشركة وفحص الدورات الحلقية عبر `accountWouldCycle(ctx, companyID, ...)`.
- الإبقاء على `UpdateAccountParent` كواجهة توافقية تستخدم سياقاً افتراضياً.
- إضافة `ctx` و`companyID` إلى واجهات التقارير في `engine.go`: `GeneralLedger` و`FinancialRatios` و`StatementBundle` و`DimensionProfitAndLossForCompanyContext`.
- إضافة `ReconcileBalances` ومحرك المطابقة `BalanceReconciliationEngine` في [`internal/reporting/reconciliation.go`](../internal/reporting/reconciliation.go).
- إضافة `ForCompany` على المحرك لضمان عدم إمكانية تجاوز نطاق الشركة في الدفاتر المساعدة.

### المرحلة 5: نضج محركات المعايير والأتمتة

- تطبيق فائدة IFRS 16 الفعلية Actual/365 مع دعم الفترات غير المنتظمة في [`internal/ifrs16/lease.go`](../internal/ifrs16/lease.go).
- إضافة `BuildStandardJobs` وبناء وظائف الأتمتة من إعداد JSON في [`internal/ifrsautomation/config.go`](../internal/ifrsautomation/config.go).
- دعم CLI عبر `agy ifrs [run|daemon] --config PATH` و`AGY_IFRS_CONFIG_PATH` في [`cmd/agy/main.go`](../cmd/agy/main.go).
- تسجيل منظم للأخطاء باستخدام `slog`، وتوثيق التشغيل في [`docs/ifrs_automation.md`](./ifrs_automation.md).

### المرحلة 6: تحسين المعمارية والجاهزية الإنتاجية

- تفكيك `StorageDriver` إلى منافذ متخصصة (ISP) في [`engine.go`](../engine.go):
  - `PostingStoragePort`
  - `ReportingStoragePort`
  - `MasterDataStoragePort`
  - `SubledgerStoragePort`
  - `GovernanceStoragePort`
- إضافة `StoragePorts` و`NewWithPorts(ports StoragePorts, config Config)` للحقن المستقل، مع `ErrStoragePortRequired` عند غياب أي منفذ إلزامي.
- إضافة تحقق عقد وقت التصريف في [`engine_storage_contract_test.go`](../engine_storage_contract_test.go) يثبت أن مشغّل PostgreSQL يفي بكل منفذ متخصص على حدة وبالواجهة المجمّعة.
- توقيع محكم النوع: `New(driver StorageDriver, config Config)` و`NewEngine(driver StorageDriver, config Config)` دون استخدام `any`.
- الإبقاء على `StorageDriver` كواجهة مجمّعة للتوافق مع المستدعين الحاليين، وتفويض `New` إلى `NewWithPorts`.
- حسم `Config`: إزالة `Config.BaseCurrency` لأن العملة الوظيفية تُقرأ من الشركة، وأصبح `Config` فارغاً مع `DefaultConfig()`.
- تنظيم الترحيلات: `EnsureSchema` يستدعي `MigrateUp` فقط، ويعتمد حصراً على سلسلة الترحيلات المرقمة والمحمية بـ Checksums. لا يوجد تنفيذ DDL عشوائي، و`schema.sql` القديم أصبح ترحيل baseline رسمياً.
- التحقق من إمكانية الرجوع: كل ترحيل له ملف `.down.sql` مقابل (`TestEveryMigrationHasRollback`).

---

## 3. مصفوفة تتبع الاختبارات (RACI Traceability)

| المهمة | الاختبار المطلوب في الخطة | الاختبار الفعلي | الملف |
| ------ | ------------------------- | --------------- | ----- |
| 1.1 | `TestMultiCurrencyTriggerPostingBalance` | مطابق | `storage/postgres/phase2_atomic_posting_test.go` |
| 1.2 | `TestConcurrentSequenceAllocation` | مطابق | `storage/postgres/bulk_posting_test.go` |
| 1.2 | `TestBulkAtomicity` | مطابق | `storage/postgres/bulk_posting_test.go` |
| 1.3 | `TestRoundingDifferenceHandling` | مطابق | `storage/postgres/rounding_test.go` |
| 2.1 | `TestApprovalAmountTamperingRejection` | مطابق | `storage/postgres/governance_integration_test.go` |
| 2.2 | `TestMakerCheckerThresholdEnforcement` | مطابق | `storage/postgres/governance_integration_test.go` |
| 2.3 | `TestAtomicPostingAuditLogCreation` | مطابق | `storage/postgres/governance_integration_test.go` |
| 3.1 | `TestEventBridgeRejectsIdempotencyPayloadConflicts` + `TestPhase3_EventBridgeIdempotencyPayloadConflictPostgres` | مطابق | `internal/subledgers/subledgers_test.go`، `storage/postgres/phase2_atomic_posting_test.go` |
| 3.2 | `TestConcurrentDisposalDraftsReserveAssetOnce` + `TestPhase3_AtomicAssetDisposalAndImpairmentPostgres` | مطابق | `internal/subledgers/subledgers_test.go`، `storage/postgres/phase2_atomic_posting_test.go` |
| 4.1 | `TestUpdateAccountParentPostgresCompanyScope` | مطابق | `storage/postgres/masterdata_parent_integration_test.go` |
| 4.2 | `TestReportFacadeWithCompanyContext` | مطابق (+ `TestReportFacadeRequiresCompanyContext` و`TestBalanceReconciliationFacadePassesCompanyScope`) | `engine_reporting_test.go` |
| 4.3 | `TestBalanceReconciliationFacadePassesCompanyScope` + `TestRoundingDifferenceHandling` | مطابق | `engine_reporting_test.go`، `storage/postgres/rounding_test.go` |
| 5.1 | `TestIrregularPeriodLeaseDiscounting` | مطابق | `internal/ifrs16/lease_test.go` |
| 5.2 | `TestCLIAutomationExecutionWithJobs` | مطابق | `cmd/agy/main_test.go` |
| 6.1-4 | `TestBaselineMigrationCleanRun` | مطابق (+ `TestNewWithPortsAcceptsIndependentStoragePorts` و`TestNewWithPortsRequiresEveryPort` و`TestEveryMigrationHasRollback`) | `storage/postgres/migrations_integration_test.go`، `engine_ports_test.go`، `storage/postgres/migrations_test.go` |

> حُدّثت مصفوفة RACI في الخطة لتستخدم أسماء الاختبارات الفعلية للمهمتين 3.1 و3.2، وأُضيف لها عمود الحالة وأدوار R/A/C/I. جميع الصفوف مغلقة ومتحققة.

---

## 4. أدلة التحقق النهائية

نُفذت الأوامر التالية على شجرة العمل النظيفة عند الالتزام `56d62a2`:

| الفحص | النتيجة |
| ----- | ------- |
| `go vet ./...` | ✅ نظيف بلا تحذيرات |
| `go test ./...` | ✅ نجاح جميع الحزم، بما فيها `storage/postgres` على قاعدة PostgreSQL حقيقية |
| `go test -race ./...` | ✅ نجاح جميع الحزم تحت كاشف التسابق |
| `TestBaselineMigrationCleanRun` | ✅ تطبيق سلسلة الترحيلات الـ19 وإعادة تشغيلها دون تعارض Checksums أو ترحيلات مكررة |
| `TestEveryMigrationHasRollback` | ✅ وجود ملف `.down.sql` لكل ترحيل |

**ملاحظة الأداء:** اختبار `TestPostgresBatchPostingPerformance` يتجاوز 2000 قيد/ثانية خارج وضع `-race`، ويتخطى فحص الأداء فقط تحت `-race` لأن أداة كشف التسابق تُبطئ القياس ولا تمثل بيئة الإنتاج.

---

## 5. القرارات المعمارية والتوافقية

1. **الإبقاء على `StorageDriver`:** بقي كواجهة مجمّعة للتوافق الخلفي، بينما أصبح `StoragePorts` + `NewWithPorts` هو المسار الموصى به للحقن المستقل وفق ISP.
2. **`Config` فارغ:** أُزيل `BaseCurrency` لأن العملة الوظيفية هي خاصية شركة وليست خاصية محرك، ما يمنع تعارضاً بين إعداد المحرك وبيانات الشركة.
3. **`EnsureSchema` = `MigrateUp`:** لا تنفيذ DDL مباشر أو عشوائي، وكل تغيير مخطط يمر عبر ترحيل مرقم مع Checksum وملف رجوع.
4. **بوابة الموافقات:** تفرض الموافقة فقط عند وجود سياسة شركة مُهيأة. غياب السياسة يحافظ على السلوك التاريخي (السماح بالترحيل) لتفادي كسر البيئات القائمة.
5. **أسماء الاختبارات:** حيث اختلف اسم الاختبار عن مصفوفة RACI، وُثّق المكافئ الوظيفي في القسم 3 بدلاً من تكرار اختبارات متطابقة تحت أسماء مختلفة.

---

## 6. إقرار الإغلاق

المراحل الست من [`docs/remediation_execution_plan.md`](./remediation_execution_plan.md) منفذة ومتحققة بالكامل. لا توجد مهام مفتوحة أو فجوات وظيفية معروفة، وقد تحققت معايير القبول والإغلاق الأربعة: خلو `go vet`، ونجاح `go test -race ./...`، ونجاح اختبارات PostgreSQL والترحيلات، وتحديث وثائق `docs/`.
