# خريطة الاعتماديات الحالية — Accounting Engine

- **المصدر:** `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./...` على الشجرة الحالية (44 package).
- **البيئة:** Go `1.26.3` linux/amd64، PostgreSQL `18.6`.
- **تاريخ القياس:** 2026-11-14.
- **الحدود:** الخريطة تثبت imports المباشرة؛ لا تثبت مسارات الاستدعاء وقت التشغيل ولا كل الاعتماديات غير المباشرة. تعتمد على حالة الشجرة قبل تعديلات P1.

## 1. مجموعات الحزم

| المجموعة | الحزم | أمثلة |
| --- | ---: | --- |
| Root public package | 1 | `accounting_engine` (`engine.go`, `engine_observability.go`, `doc.go`) |
| `pkg/api/v1` | 4 | `events`, `ledger`, `reporting`, `types` |
| `pkg/sdk` | 2 | `sdk`, `sdk/templates` |
| `pkg/spi` | 4 | `events`, `masterdata`, `standards`, `storage` |
| `internal` | 21 | `posting`, `masterdata`, `reporting`, `subledgers`, `governance`, `sdkapplication`, ... |
| `storage` | 3 | `postgres`, `memory`, `masterdatacontract` |
| `modules` | 4 | `ifrs9`, `ifrs15`, `ifrs16`, `ias12` |
| `cmd` | 2 | `accounting-service`, `agy` |
| `proto` | 1 | `proto/accounting/v1` |
| `test` | 3 | `acid`, `external_consumer`, `service_integration` |

`storage/postgres` يحوي 48 ملف migration و17 ملفًا يعرّف Triggers/RLS Policies. هذه العمليات هي مصدر الحماية الفعلية المتوازية مع قواعد Domain، ويجب مراجعتها في P3-02 وP6-03.

## 2. اتجاه الاعتماديات بين الحدود

| المصدر | يستورد مباشرة من | تقييم الاتجاه |
| --- | --- | --- |
| `pkg/api/v1/*` | `pkg/api/v1/*` فقط | سليم |
| `pkg/spi/*` | `pkg/api/v1/*` و`pkg/spi/storage` | سليم جزئيًا؛ `standards` يعتمد على `CoreLedgerStore` المركب |
| `pkg/sdk` | `internal/masterdata`, `internal/posting`, `pkg/api/v1`, `pkg/spi`, `storage/memory`, `storage/postgres` | **تسرب**: Public SDK يستورد Internal وAdapters مدمجة |
| `modules/*` | `pkg/*`, `storage/memory` | تسرب Adapter مدمج إلى وحدات معيارية |
| `storage/postgres` | `internal/*` (posting, masterdata, reporting, governance, policy, subledgers, valuation, sdkapplication) | **تبعية عكسية**: Adapter يستورد Domain/Application بدل تنفيذ Ports |
| `storage/memory` | `internal/*` بنفس النمط | نفس المشكلة |
| `internal/*` | `pkg/api/v1`, `pkg/spi`, وبعض `internal` | سليم داخل الحد الداخلي |
| `cmd/agy` | Root `accounting_engine` + `internal/*` | يستخدم API قديمًا غير مستقر |
| `cmd/accounting-service` | `pkg/sdk`, `internal/*`, `storage/*`, `proto` | يخلط Public وInternal |
| `test/external_consumer` | `pkg/*` و`storage/memory` | لا يثبت Adapter خارجيًا مستقلًا |

## 3. Root Package — تحليل التصدير

`go doc -all .` يظهر الأنواع المصدرة التالية، وكلها تعرض أنواعًا داخلية أو مركبة:

- `Config.Standards StandardsConfig` حيث `StandardsConfig` يحوي `*ifrs9.Unit`, `*ifrs15.Unit`, `*ifrs16.Unit`, `*ias12.Unit`, `*consolidation.Unit` من `internal/*`.
- `PolicyConfig` يحوي `policy.AccountingManagerAuthorizer` و`policy.BackdatingExceptionAuthorizer` من `internal/policy`.
- `ObservabilityConfig.Metrics *observability.Metrics` و`AccountingEngine.Metrics()`.
- `StorageDriver` و`StoragePorts` يعرّفان واجهات داخلية (`posting.BulkRepository`, `reporting.Repository`, `masterdata.ContextStorageRepository`, `subledgers.Repository`, `governance.Repository`, `valuation.RateProviderSPI`).
- `AccountingEngine.ForCompany(...) *subledgers.CompanySubledgerService` و`SaveCostCenter(ctx, masterdata.CostCenter)` وغيرها.
- `New(driver StorageDriver, config Config)` يعني أن المستهلك الخارجي مطالَب بتنفيذ كل الواجهات الداخلية دفعة واحدة.

**الخلاصة:** Root package ليس Facade؛ هو تنفيذ المحرك كاملًا موجهًا للمستهلك الخارجي. لذلك لا يمكن إبقاؤه عامًا دون كسر صحة P1-01/P1-05، ويحتاج قرارًا معماريًا (ADR-0002) للتحويل إلى Facade مهمَل يستدعي `pkg/sdk` فقط.

## 4. تسريبات `internal` في الحزم العامة

| الحزمة العامة | الاستيراد الداخلي | الأثر |
| --- | --- | --- |
| `accounting_engine` (root) | `internal/consolidation`, `governance`, `ias12`, `ifrs15`, `ifrs16`, `ifrs9`, `ifrsautomation`, `masterdata`, `observability`, `policy`, `posting`, `reporting`, `subledgers`, `valuation` | المستهلك لا يستطيع استيراد الجذر من خارج الوحدة لأن Go تمنع `internal` عبره؛ أي أن Root API غير قابل للاستهلاك الخارجي فعليًا |
| `pkg/sdk` | `internal/masterdata`, `internal/posting` | نفس المشكلة: استيراد `pkg/sdk` خارجيًا يفشل لأن الحزمة تستورد `internal` |
| `pkg/sdk/templates` | (يمرر عبر `pkg/sdk`) | يرث المشكلة |
| `modules/*` | `storage/memory` | تثبيت Adapter واحد في وحدة اختيارية |

**ملاحظة حاسمة:** `scripts/check_public_api_purity.sh` يفحص `./pkg/...` فقط، ولذلك ينجح الآن رغم أن `pkg/sdk` يستورد `internal`. نجاح الفحص الحالي لا يعني نقاء العقود العامة.

## 5. عمليات الكتابة والقيود الفعلية (لتنسيق P3)

- `storage/postgres/posting.go`, `bulk_posting.go`, `ledger_application.go`: كتابة رأس القيد والسطور وحالة القيد داخل معاملة.
- `storage/postgres/events.go`, `public_events.go`: حجز Idempotency للحدث.
- `storage/postgres/governance.go`, `audit_chain*`: سلسلة تدقيق SHA-256 مع Triggers تمنع UPDATE/DELETE/TRUNCATE.
- `storage/postgres/migrations/*`: Foreign Keys, CHECK constraints, Unique indexes, Triggers, RLS.
- `storage/memory/*`: محاكاة المعاملات والأقفال داخل العملية.

هذه الملفات عالية التداخل ولا يجوز تعديلها من أكثر من مهمة متوازية. تنسيق Schema مطلوب قبل P4/P6.

## 6. الملفات المشتركة عالية الحساسية

| الملف/الحزمة | المهام المالكة | قاعدة التنسيق |
| --- | --- | --- |
| `pkg/api/v1/**`, `pkg/spi/**` | P1 | تُجمّد عقودها قبل بدء P2/P3؛ أي تغيير بعدها يحتاج ADR |
| `pkg/sdk/builder.go`, `engine.go` | P1-04/P1-05 | لا تُعدّل من مهام الوحدات |
| `internal/sdkapplication/**` | P2 | مملوك لمهام P2 |
| `internal/posting/**` | P2/P4 | أي تغيير في Posting Policy يلزمه اختبار PostgreSQL وMemory |
| `storage/postgres/**` | P3/P4/P6 | Schema واحد في كل مرة؛ تسجيل في ARD |
| `proto/accounting/v1/**` | P6 | لا يُعدّل بالتوازي مع gRPC handler |
| `test` harness | P3-07 | كل Adapter يضيف اختباراته دون تغيير قواعد المقارنة |

## 7. مسارات تحتاج إصلاحًا (مرتبطة بالمهام)

1. `pkg/sdk/builder.go` → Type Assertion `LedgerApplicationProvider` → `storage.postgres.NewLedgerApplication` (P1-04).
2. `pkg/sdk/builder.go` → Type Assertion `MasterDataStore()` (P1-04).
3. Root `accounting_engine.New/NewWithPorts` → `internal/posting` etc. (P1-05).
4. `internal/sdkapplication.PostEntry` → posts `entry.CreatedBy` كـ poster (P2-04/P2-05).
5. `internal/sdkapplication.ReverseEntry` → يستخدم `reason` كـ creator/poster (P2-05).
6. `storage/postgres.NewLedgerApplication` → ينشئ Application Service من داخل Adapter (P3-02).
7. ~~Event ingestion fallback idempotency في الذاكرة~~ (P3-04): أُغلق؛ السجل دائم عبر `storage.Ports.Events` بهوية `(شركة, مصدر, مفتاح)` وبصمة حمولة مُصدّرة، ولا يوجد Fallback داخل العملية.
