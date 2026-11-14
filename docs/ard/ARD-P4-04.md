# ARD-P4-04: تدقيق Reversal محاسبيًا واختبار دورة العكس الكاملة

- **المهمة:** `P4-04`
- **المرحلة:** `P4`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة
- **النطاق:** `internal/posting/reversal*`، `internal/adapters/postgres/posting.go`، `migrations`، واختبارات دورة العكس في `reversal_lifecycle_test.go` و`p4_04_reversal_test.go`.

## 1. السياق والمشكلة

كان المحرك المحاسبي يوفر دالة عكس أساسية وربطاً بين قيدين، لكن ذلك لم يكن كافياً لإثبات صحة العكس محاسبياً وقانونياً والتزاماً بمعايير الرقابة والتدقيق:

1. **انقطاع رابط المطابقة:** رقم المطابقة (`MatchingNumber`) لم يكن يُنسخ إلى سطر العكس، رغم أن التحقق `ValidateReversalLines` يشترط تطابقه، ما كان يجعل أي عكس لقيد يحمل رقم مطابقة غير متسق محاسبياً.
2. **غياب التحقق الزمني:** لم يكن هناك تحقق يمنع تحديد تاريخ عكس صريح يسبق تاريخ وقوع القيد الأصلي.
3. **غياب إلزامية الفاعل:** لم يكن `ActorID` مطلوباً بشكل صارم، مما فتح ثغرة لبناء قيود عكس بلا هوية مسؤولة في سجل التدقيق.
4. **هشاشة منع التكرار تحت التزامن:** لم تكن ضمانة "عكس واحد فقط لكل أصل" مدعومة بفهرس فريد على مستوى قاعدة البيانات PostgreSQL.
5. **عدم دقة التوصيف الدلالي في التدقيق:** كان سجل التدقيق في PostgreSQL يسجل قيد العكس كـ `POST` بدلاً من `REVERSE`، مما يضلل تقارير التدقيق المحاسبي.

## 2. القرارات والتنفيذ

1. **حفظ الهوية المحاسبية للأصل والأبعاد التحليلية بالكامل:**
   - يُنسخ `MatchingNumber` من كل سطر أصلي إلى سطر العكس كمرجع توثيقي يربط الزوج.
   - يُصفَّر `ReconciledAmount` صراحةً (0.00) على سطر العكس؛ لأن العكس ليس حدث تسوية ولا يجوز أن يرث حالة مطابقة سابقة.
   - تُحفظ كافة الحسابات والأبعاد التحليلية الأصلية بدقة 1:1: (`AccountID`, `PartnerID`, `CostCenterID`, `ProjectID`, `SegmentID`, `BusinessUnitID`).
   - لا يُسمح بإدخال حسابات أو أبعاد بديلة؛ أي اختلاف يُرفض بـ `ErrInvalidReversal`.

2. **نسخ سياق العملة ومطابقة الأثر الصافي (Net Zero Invariant):**
   - يُنسخ `TransactionCurrency` و`ExchangeRate` من القيد الأصلي لضمان اتساق التقارير المحاسبية.
   - المبالغ الوظيفية تُعكس بدقة: `FunctionalDebit = Original.FunctionalCredit` و`FunctionalCredit = Original.FunctionalDebit`.
   - الأثر الصافي التراكمي للأصل والعكس عبر جميع الحسابات والعملات والأبعاد يساوي صفراً تماماً.

3. **الحدود الصارمة لما لا يُورث:**
   - لا يرث قيد العكس: `ApprovalFingerprint`، `SourceEventID`، `LegalNumber`، `SequenceNumber`، ولا المرفقات. قيد العكس هو وثيقة قانونية جديدة تستحق دورة اعتماد وترقيم مستقلين.

4. **سياسة التاريخ والفترات المالية:**
   - **الافتراضي:** وقت التنفيذ الحالي `time.Now().UTC()`، مما يسمح بعكس القيود المستقبلية فور ترحيلها.
   - **التاريخ الصريح:** يُمنع تقديم تاريخ العكس عن تاريخ القيد الأصلي، ويُرفض بـ `ErrReversalDateBeforeOriginal` مصنف كخطأ تحقق (`Validation` / `CodeInvalidEntry`).
   - **الفترات:** يُرفض العكس في فترة مغلقة صلباً بـ `ErrFiscalPeriodClosed`. عند إغلاق فترة الأصل، يُنفذ العكس في فترة لاحقة مفتوحة ويُسجل بـ `PeriodID` الخاص بالفترة المفتوحة.

5. **إلزامية الفاعل وسجل التدقيق الدائم:**
   - أصبح `ActorID` إلزامياً لطلبات العكس، ويُرفض الفاعل الفارغ بـ `ErrReversalActorRequired`.
   - تُسجل العملية في `financial_audit_logs` بإجراء `REVERSE` لكلا الطرفين (الأصل والعكس) مع حفظ الفاعل، السبب، المرجع، ومفتاح Idempotency.

6. **منع العكس المزدوج والتزامن الشديد:**
   - أُضيفت migration برقم `202611140005_reversal_integrity.sql` بفهرس فريد جزئي:

     ```sql
     CREATE UNIQUE INDEX IF NOT EXISTS uq_account_moves_reversal_original
         ON account_moves (company_id, (entry->>'OriginalEntryID'))
         WHERE classification = 'reversing'
           AND NULLIF(entry->>'OriginalEntryID', '') IS NOT NULL;
     ```

   - دعم كامل لـ `IdempotencyKey`: الطلبات المتزامنة بنفس المفتاح والحمولة تعيد نفس قيد العكس بنجاح (`replay = true`).
   - الطلبات المتزامنة بمفاتيح مختلفة تعيد نجاح عملية واحدة فقط وفشل الباقي قطعيّاً بـ `ErrEntryAlreadyReversed`.

---

## 3. إثبات الاختبارات

### أوامر التشغيل والنتائج

#### اختبارات وحدة المحرك (`internal/posting`)

```text
$ go test -v ./internal/posting/... -run TestReversal
=== RUN   TestReversal
--- PASS: TestReversal (0.00s)
=== RUN   TestReversalIdempotencyPersistsReasonAndActor
--- PASS: TestReversalIdempotencyPersistsReasonAndActor (0.00s)
=== RUN   TestReversalRepositoryFailureLeavesNoDraftOrOriginalMutation
--- PASS: TestReversalRepositoryFailureLeavesNoDraftOrOriginalMutation (0.00s)
=== RUN   TestReversalWithMakerCheckerThreshold
--- PASS: TestReversalWithMakerCheckerThreshold (0.00s)
=== RUN   TestReversalNetZeroInvariantTable
=== RUN   TestReversalNetZeroInvariantTable/Simple_2-line_single_currency_with_analytical_dimensions
=== RUN   TestReversalNetZeroInvariantTable/Compound_3-line_entry_(Receivable,_Revenue,_VAT)
=== RUN   TestReversalNetZeroInvariantTable/Multi-currency_entry_with_foreign_exchange_rates
--- PASS: TestReversalNetZeroInvariantTable (0.00s)
=== RUN   TestReversalPreventsDraftReversal
--- PASS: TestReversalPreventsDraftReversal (0.00s)
=== RUN   TestReversalPreventsAlreadyReversedReversal
--- PASS: TestReversalPreventsAlreadyReversedReversal (0.00s)
=== RUN   TestReversalTenantIsolation
--- PASS: TestReversalTenantIsolation (0.00s)
=== RUN   TestReversalRejectsEffectiveDateBeforeOriginal
--- PASS: TestReversalRejectsEffectiveDateBeforeOriginal (0.00s)
=== RUN   TestReversalMatchingNumberAndReconciledAmountLifecycle
--- PASS: TestReversalMatchingNumberAndReconciledAmountLifecycle (0.00s)
=== RUN   TestReversalAuditAndMetadataPersistence
--- PASS: TestReversalAuditAndMetadataPersistence (0.00s)
PASS
ok   github.com/osmai4603-collab/accounting_engine/internal/posting 0.010s
```

#### اختبارات التكامل على PostgreSQL (`internal/adapters/postgres`)

```text
$ go test -v ./internal/adapters/postgres/... -run TestP404
=== RUN   TestP404ReversalOnPostgres
--- PASS: TestP404ReversalOnPostgres (0.44s)
=== RUN   TestP404ReversalInLaterOpenPeriod
--- PASS: TestP404ReversalInLaterOpenPeriod (0.39s)
=== RUN   TestP404ConcurrentReversalHighContention
--- PASS: TestP404ConcurrentReversalHighContention (0.49s)
=== RUN   TestP404ConcurrentIdempotencyReplay
    p4_04_reversal_test.go:353: Concurrent idempotency replay succeeded: 16 goroutines, 15 replays, reversal ID ee15b506-b456-4174-8b02-ea30add162b0
--- PASS: TestP404ConcurrentIdempotencyReplay (0.63s)
=== RUN   TestP404PersistenceAndRetrieval
--- PASS: TestP404PersistenceAndRetrieval (0.40s)
PASS
ok   github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres 2.366s
```

#### اختبارات سلامة المهاجرات (Migrations Integrity)

```text
$ go test -v ./internal/adapters/postgres/... -run "MigrationsHaveReversiblePairs|DangerousMigrationsRequireApproval"
=== RUN   TestMigrationsHaveReversiblePairs
--- PASS: TestMigrationsHaveReversiblePairs (0.00s)
=== RUN   TestDangerousMigrationsRequireApproval
--- PASS: TestDangerousMigrationsRequireApproval (0.02s)
PASS
ok   github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres 0.025s
```

#### إعادة التحقق في مساحة العمل الحالية

أُعيد تشغيل اختبارات PostgreSQL ذات الصلة بعد استكمال التحقق من التراجع الذري. نجحت اختبارات العكس على PostgreSQL، والتزامن، وإعادة التشغيل، وتراجع المعاملة عند فشل ربط الأصل، وسلامة أزواج المهاجرات.

```text
$ go test -v ./internal/adapters/postgres/... -run 'Test(P404|PostgresReversal|MigrationsHaveReversiblePairs|DangerousMigrationsRequireApproval)'
PASS
ok github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres 10.994s
```

نجحت اختبارات وحدة `internal/posting` واختبارات PostgreSQL ذات الصلة على الحالة النهائية لمساحة العمل.

---

### مصفوفة معايير القبول (Acceptance Criteria Matrix)

| المعيار | ملف الاختبار | الدالة الاختبارية | النتيجة |
| --- | --- | --- | --- |
| وثيقة مواصفة العكس المحاسبي المكتملة | `docs/accounting/reversal_specification.md` | وثيقة معتمدة ومطابقة لجميع البنود | معتمدة وموثقة |
| التحقق من ثبات الأثر الصافي (Net Zero Invariant) | `internal/posting/reversal_lifecycle_test.go` | `TestReversalNetZeroInvariantTable` | ناجح (3 سيناريوهات) |
| منع عكس المسودة (Draft) | `internal/posting/reversal_lifecycle_test.go` | `TestReversalPreventsDraftReversal` | ناجح (`ErrEntryNotPosted`) |
| منع عكس قيد معكوس مسبقاً | `internal/posting/reversal_lifecycle_test.go` | `TestReversalPreventsAlreadyReversedReversal` | ناجح (`ErrEntryAlreadyReversed`) |
| منع عكس قيد شركة أخرى (عزل المستأجرين) | `internal/posting/reversal_lifecycle_test.go` | `TestReversalTenantIsolation` | ناجح (`ErrEntryNotFound`) |
| رفض تاريخ صريح يسبق تاريخ الأصل | `internal/posting/reversal_lifecycle_test.go` | `TestReversalRejectsEffectiveDateBeforeOriginal` | ناجح (`ErrReversalDateBeforeOriginal`) |
| حفظ MatchingNumber وتصفير ReconciledAmount | `internal/posting/reversal_lifecycle_test.go` | `TestReversalMatchingNumberAndReconciledAmountLifecycle` | ناجح (تصفير العكس وحفظ الأصل) |
| حفظ السبب والفاعل والمرجع في القيد والتدقيق | `internal/posting/reversal_lifecycle_test.go` | `TestReversalAuditAndMetadataPersistence` | ناجح وحفظ كافة الحقول |
| العكس على PostgreSQL وتصفير سطور الحسابات | `internal/adapters/postgres/p4_04_reversal_test.go` | `TestP404ReversalOnPostgres` | ناجح (تصفير `account_move_lines`) |
| العكس في فترة لاحقة مفتوحة بعد إقفال فترة الأصل | `internal/adapters/postgres/p4_04_reversal_test.go` | `TestP404ReversalInLaterOpenPeriod` | ناجح (رفض مارس وقبول أبريل) |
| منع التكرار تحت التزامن الشديد (16 Goroutines) | `internal/adapters/postgres/p4_04_reversal_test.go` | `TestP404ConcurrentReversalHighContention` | ناجح (نجاح 1 وفشل 15 بـ AlreadyReversed) |
| دعم IdempotencyKey تحت التزامن الشديد | `internal/adapters/postgres/p4_04_reversal_test.go` | `TestP404ConcurrentIdempotencyReplay` | ناجح (نجاح 16 وإعادة نفس القيد) |
| استرجاع تفاصيل القيد والعكس والتدقيق بعد Restart | `internal/adapters/postgres/p4_04_reversal_test.go` | `TestP404PersistenceAndRetrieval` | ناجح (بقاء البيانات وسلسلة التدقيق) |
| فشل الربط الذري لا يترك قيد عكس أو سجل REVERSE | `internal/adapters/postgres/reversal_atomic_test.go` | `TestPostgresReversalFailureRollsBackDraftAndOriginalLink` | ناجح (لا تغيير للأصل أو السجل التدقيقي) |

---

## 4. الملفات المتأثرة والمضافة

- `docs/accounting/reversal_specification.md` (مواصفة العكس المحاسبي الكاملة)
- `docs/architecture/reversal_specification.md` (المواصفة المعمارية لدورة العكس)
- `docs/ard/ARD-P4-04.md` (ملف ARD للقرارات والاختبارات)
- `internal/posting/reversal.go` و`internal/posting/reversal_request.go` و`internal/posting/reversal_validation.go` (بناء العكس والبصمة والتحقق)
- `internal/posting/reversal_lifecycle_test.go` (اختبارات دورة العكس وثبات الأثر الصافي)
- `internal/adapters/postgres/p4_04_reversal_test.go` (اختبارات دورة العكس والتزامن والفترات على PostgreSQL)
- `internal/adapters/postgres/reversal_atomic_test.go` (اختبارات ذرية العكس والتراجع عند الفشل)
- `internal/adapters/postgres/migrations/202611140005_reversal_integrity.sql` (فهرس منع العكس المكرر)
- `internal/adapters/postgres/migrations/202611140005_reversal_integrity.down.sql` (التراجع عن الفهرس)
