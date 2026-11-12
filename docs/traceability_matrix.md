# مصفوفة تتبع الفجوات والاختبارات

- **الحالة:** نشطة
- **التاريخ:** 2026-11-12
- **المالك:** `QA` + `LEDGER`
- **المهمة:** `P0-T4`
- **المرجع:** [`index.md`](./index.md) — سجل الفجوات `GAP-*`، و[`phase-0-baseline-and-governance.md`](./phase-0-baseline-and-governance.md)

## 1. الغرض وقاعدة الإغلاق

هذه المصفوفة تربط كل فجوة في سجل `index.md` بدليل اختباري محدد. القاعدة الملزمة:

1. كل فجوة `Critical` أو `High` لها اختبار واحد على الأقل.
2. الاختبار يجب أن **يفشل على الكود الحالي** (أحمر) أو يكون `t.Skip` مبرراً بسبب عدم وجود واجهة/عقد بعد.
3. الاختبارات الحمراء تحمل وسم بناء `gapred` حتى يبقى خط CI الافتراضي أخضر قبل بدء الإصلاح.
4. اسم الاختبار يبدأ بـ`TestGAP_<AREA>_<NN>` ويحمل رسالة الفشل معرّف الفجوة.
5. عند إصلاح الفجوة في مرحلة لاحقة، يُزال وسم `gapred` أو يُحوَّل الاختبار إلى اختبار انحدار دائم.

## 2. أوامر التشغيل

```bash
# الاختبارات الافتراضية (خضراء، ولا تتضمن اختبارات الفجوات الحمراء)
go test ./...

# اختبارات الفجوات الحمراء فقط (يجب أن تفشل عند نقاط الفجوات المعلنة)
go test -count=1 -tags gapred -run TestGAP ./...

# فجوة محددة
go test -count=1 -tags gapred -run TestGAP_PST_02 ./internal/posting
```

> اختبارات `storage/postgres` تتطلب PostgreSQL. محلياً استخدم:
> `postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql`
> وفي CI تُوفَّر خدمة `postgres:18.6` تلقائياً.

## 3. الفجوات الحرجة والعالية (شرط G0)

الرموز: 🔴 فاشل على الكود الحالي | ⏭️ `Skip` مبرر | ✅ اختبار انحدار ناجح بعد الإصلاح

| GAP ID | الخطورة | الوصف المختصر | الموقع | الاختبار | النوع | الحالة |
| --- | --- | --- | --- | --- | --- | --- |
| `GAP-GOV-01` | Critical | PostgreSQL يسمح بإعادة فتح فترة مقفلة نهائياً | `storage/postgres/masterdata.go::UpdatePeriodState` | `TestGAP_GOV_01_HardClosedPeriodCannotBeReopened` (`storage/postgres/gapred_test.go`) | تكامل PostgreSQL | 🔴 |
| `GAP-GOV-02` | High | إعادة فتح الفترة بلا صلاحية/سبب/تدقيق | `internal/masterdata/fiscal_calendar.go` | `TestGAP_GOV_02_ReopenRequiresAuthorizationAndAudit` (`internal/governance`, `internal/posting`) | وحدة/فشل | ⏭️ — لا توجد واجهة إعادة فتح بعد (`P2-T4`) |
| `GAP-GOV-03` | High | غياب السياسة يُفسَّر كسماح (Fail-Open) | `internal/governance/approval_gate.go`, `engine.go` | `TestGAP_GOV_03_UnconfiguredGateFailsClosed` (`internal/governance/gapred_test.go`) | وحدة/فشل | 🔴 |
| `GAP-PST-01` | Critical | حالة الفترة تُقرأ خارج المعاملة ولا تُقفل داخلها | `internal/posting/engine.go`, `storage/postgres/posting.go` | `TestGAP_PST_01_PostingRechecksPeriodInsideTransaction` (`storage/postgres/gapred_test.go`) | تكامل/تزامن | 🔴 |
| `GAP-PST-02` | High | `DraftContentMatches` يغفل الفترة/الفترة المستهدفة/التفويض/القيد الأصلي | `internal/posting/entry.go` | `TestGAP_PST_02_DraftContentMatchesPeriodAndAuthorization` (`internal/posting/gapred_test.go`) | وحدة/فشل (4 حالات فرعية) | 🔴 |
| `GAP-PST-03` | High | بصمة الاعتماد لا تغطي `SourceEventID` | `internal/posting/content_hash.go` | `TestGAP_PST_03_EntryContentHashCoversSourceEventID` (`internal/posting/gapred_test.go`) | وحدة | 🔴 |
| `GAP-PST-04` | High | مسار العكس الاحتياطي غير ذرّي | `internal/posting/reversal.go` | `TestGAP_PST_04_ReversalFallbackIsNotAtomic` (`internal/posting/gapred_test.go`) | وحدة/فشل | 🔴 |
| `GAP-PST-05` | High | عكس PostgreSQL لا يثبت تطابق الأسطر مع الأصل | `storage/postgres/posting.go::PostReversalAtomicContext` | `TestGAP_PST_05_ReversalMustInvertOriginalLines` (`storage/postgres/gapred_test.go`) | تكامل PostgreSQL | 🔴 |
| `GAP-PST-06` | High | الترحيل الجماعي يتراجع لمسار متسلسل غير ذرّي | `internal/posting/bulk.go::PostBulkContext` | `TestGAP_PST_06_BulkFallbackIsNotAtomic` (`internal/posting/gapred_test.go`) | وحدة/فشل | 🔴 |
| `GAP-PST-07` | High | حل الفترة خارج معاملة الدفعة | `internal/posting/bulk.go`, `prepareForPosting` | `TestGAP_PST_07_BulkResolvesPeriodInsideTransaction` (`storage/postgres/gapred_test.go`) | تكامل PostgreSQL | ⏭️ — يتطلب واجهة الدفعة الذرّية (`P1-T6`) |
| `GAP-AST-01` | High | `ValidateAttachment` يتحقق من المعرفات فقط | `internal/subledgers/fixed_assets.go` | `TestGAP_AST_01_DepreciationAttachmentValidatesEntryContent` (`internal/subledgers/gapred_test.go`) | وحدة/فشل | 🔴 |
| `GAP-AST-02` | High | استهلاك متزامن لنفس الأصل ينتج قيدين | `internal/subledgers/fixed_assets.go::DepreciateAssetAtomic` | `TestGAP_AST_02_ConcurrentDepreciationOfSameAssetPostsOnce` (`storage/memory/gapred_test.go`) | تزامن/سباق | 🔴 |
| `GAP-CLS-01` | High | تأكيد الإقفال يتحقق من البيانات الوصفية لا محتوى القيد | `internal/reporting/closing_engine.go` | `TestGAP_CLS_01_ConfirmRejectsContentMismatchedClosingEntry` (`internal/reporting/gapred_test.go`) | وحدة/فشل | 🔴 |
| `GAP-CLS-02` | High | إغلاق الفترات وتحديث السنة غير ذرّي | `internal/reporting/closing_engine.go` | `TestGAP_CLS_02_CloseIsAtomicOnPartialFailure` (`internal/reporting/gapred_test.go`) | وحدة/فشل | 🔴 |
| `GAP-STD-01` | High | اعتراف تلقائي بالأصل الضريبي المؤجل عند `nil` | `internal/ias12/ias12.go` | `TestGAP_STD_01_NilFutureProfitMustNotRecognizeDTA` (`internal/ias12/gapred_test.go`) | وحدة/فشل | 🔴 |
| `GAP-STD-02` | High | رفض المعدلات المنفذة بتاريخ مستقبلي | `internal/ias12/ias12.go::TaxRate.Validate` | `TestGAP_STD_02_EnactedFutureRateIsUsable` (`internal/ias12/gapred_test.go`) | وحدة | 🔴 |
| `GAP-OPS-01` | High | الخدمة تعرض `/healthz` و`/metrics` فقط | `cmd/agy/serve.go` | `TestGAP_OPS_01_ServiceExposesReadinessProbe` (`cmd/agy/gapred_test.go`) | وحدة/HTTP | ⏭️ — ضمن `P4-T1` |
| `GAP-OPS-02` | High | جدولة الأتمتة بلا حالة مستدامة/إعادة محاولة/DLQ | `internal/ifrsautomation/service.go` | `TestGAP_OPS_02_SchedulerHasDurableState` (`internal/ifrsautomation/gapred_test.go`) | وحدة/فشل | ⏭️ — ضمن `P4-T2` |
| `GAP-ERP-01` | High | لا دفتر مخزون/تقييم/تكلفة بضاعة مستقل | `internal/` | `TestGAP_ERP_01_InventorySubledgerExists` (`gapred_test.go`) | هيكلي | ⏭️ — ضمن `P5-T2` |
| `GAP-ERP-02` | High | لا وحدة رواتب/استحقاقات/استقطاعات | `internal/` | `TestGAP_ERP_02_PayrollModuleExists` (`gapred_test.go`) | هيكلي | ⏭️ — ضمن `P5-T5` |

## 4. الفجوات المتوسطة والمنخفضة

هذه الفجوات **خارج شرط G0 الحرج**، لكنها مسجلة حتى لا تُفقد. تُحوَّل إلى اختبار أحمر عند بدء مهمتها في المرحلة المعنية.

| GAP ID | الخطورة | الاختبار/الحالة | المهمة |
| --- | --- | --- | --- |
| `GAP-GOV-04` | Medium | لا اختبار بعد؛ تُغطى مع `GAP-GOV-03` عند إصلاح `P1-T7` | `P1-T7` |
| `GAP-PST-08` | Medium | `TestGAP_PST_08_NoIdempotencyKey` — ⏭️ لا حقل `IdempotencyKey` بعد | `P1-T10` |
| `GAP-PST-09` | Medium | لا اختبار بعد؛ تُدمج مع `GAP-PST-01` في `P1-T2` | `P1-T2` |
| `GAP-PST-10` | Medium | `TestGAP_PST_10_NoOptimisticVersion` — ⏭️ لا حقل `Version` بعد | `P1-T11` |
| `GAP-AST-03` | Medium | `TestGAP_AST_03_AssetUpdateUsesOptimisticVersion` — ⏭️ لا نسخة متوقعة بعد | `P1-T9` |
| `GAP-AST-04` | Medium | لا اختبار بعد؛ يُصحح مع `GAP-AST-02` | `P1-T9` |
| `GAP-CLS-03` | Medium | لا اختبار بعد | `P2-T7` |
| `GAP-CLS-04` | Medium | لا اختبار بعد | `P2-T3` |
| `GAP-CLS-05` | Medium | لا اختبار بعد | `P2-T6` |
| `GAP-STD-03` | Medium | لا اختبار بعد | `P3-T3` |
| `GAP-STD-04` | Medium | لا اختبار بعد | `P3-T4` |
| `GAP-STD-05` | Medium | لا اختبار بعد | `P3-T5` |
| `GAP-STD-06` | Medium | لا اختبار بعد | `P3-T6` |
| `GAP-STD-07` | Medium | لا اختبار بعد | `P3-T7` |
| `GAP-STD-08` | Low | لا اختبار بعد | `P3-T8` |
| `GAP-STD-09` | Medium | لا اختبار بعد | `P3-T10` |
| `GAP-STD-10` | Low | لا اختبار بعد | `P3-T11` |
| `GAP-OPS-03` | Medium | لا اختبار بعد | `P4-T3` |
| `GAP-OPS-04` | Medium | لا اختبار بعد | `P4-T5` |
| `GAP-OPS-05` | Medium | لا اختبار بعد | `P4-T7` |
| `GAP-OPS-06` | Medium | لا اختبار بعد | `P4-T4` |
| `GAP-OPS-07` | Medium | `storage/postgres/rls_coverage_test.go` يغطي 27 جدولاً مالياً في `P0-T7`؛ يبقى التشغيل المستمر في CI | `P4-T6` |
| `GAP-OPS-08` | Low | لا اختبار بعد | `P4-T8` |
| `GAP-OPS-09` | Low | ✅ عولجت في `P0-T5`؛ `docs/README.md` بلا روابط مكسورة | `P0-T5` |
| `GAP-ERP-03` | Medium | لا اختبار بعد | `P5-T3` |
| `GAP-ERP-04` | Medium | لا اختبار بعد | `P5-T4` |
| `GAP-ERP-05` | Medium | لا اختبار بعد | `P5-T6` |
| `GAP-ERP-06` | Medium | لا اختبار بعد | `P5-T7` |
| `GAP-ERP-07` | Medium | لا اختبار بعد | `P5-T8` |
| `GAP-ERP-08` | Medium | لا اختبار بعد | `P5-T9` |
| `GAP-ERP-09` | Medium | لا اختبار بعد | `P5-T10` |
| `GAP-ERP-10` | Low | لا اختبار بعد | `P5-T11` |

## 5. سجل Skip المبرر

| الاختبار | سبب التخطي | شرط الإزالة |
| --- | --- | --- |
| `TestGAP_GOV_02_ReopenRequiresAuthorizationAndAudit` | لا واجهة إعادة فتح فترة بعد | عند إضافة عقد `P2-T4` |
| `TestGAP_PST_07_BulkResolvesPeriodInsideTransaction` | لا واجهة دفعة ذرّية بعد | عند إضافة `BulkRepository` في `P1-T6` |
| `TestGAP_PST_08_NoIdempotencyKey` | `JournalEntry` بلا `IdempotencyKey` | عند إضافة `P1-T10` |
| `TestGAP_PST_10_NoOptimisticVersion` | `JournalEntry` بلا `Version` | عند إضافة `P1-T11` |
| `TestGAP_AST_03_AssetUpdateUsesOptimisticVersion` | التحديث غير مشروط بلا نسخة متوقعة | عند إضافة `P1-T9` |
| `TestGAP_OPS_01_ServiceExposesReadinessProbe` | لا `/readyz`/auth/TLS/limits | عند `P4-T1` |
| `TestGAP_OPS_02_SchedulerHasDurableState` | الجدولة في الذاكرة فقط | عند `P4-T2` |
| `TestGAP_ERP_01_InventorySubledgerExists` | لا وحدة مخزون | عند `P5-T2` |
| `TestGAP_ERP_02_PayrollModuleExists` | لا وحدة رواتب | عند `P5-T5` |

## 6. دليل التشغيل الحالي

آخر تشغيل لاختبارات الفجوات (2026-11-12) أنتج الفشل المتوقع في:

```text
internal/governance   TestGAP_GOV_03
internal/ias12        TestGAP_STD_01, TestGAP_STD_02
internal/posting      TestGAP_PST_02, TestGAP_PST_03, TestGAP_PST_04, TestGAP_PST_06
internal/reporting    TestGAP_CLS_01, TestGAP_CLS_02
internal/subledgers   TestGAP_AST_01
storage/memory        TestGAP_AST_02
storage/postgres      TestGAP_GOV_01, TestGAP_PST_01, TestGAP_PST_05
```

والاختبارات المتخطاة ظهرت `ok` مع `t.Skip` المبرر أعلاه. هذا يثبت أن:

- كل فجوة `Critical`/`High` لها دليل اختباري.
- الفشل يحدث على الكود الحالي، وليس بعد إصلاح مسبق.
- الخط الافتراضي `go test ./...` يبقى أخضر لأن وسم `gapred` غير مفعّل.

## 7. المراجعة

تُراجع هذه المصفوفة أسبوعياً مع `ARCH` أثناء المراحل 1–5. أي فجوة جديدة تدخل `index.md` يجب أن تضيف صفاً هنا قبل بدء الإصلاح. وأي فجوة تُغلق يجب أن تتحول من 🔴/⏭️ إلى ✅ مع رابط الالتزام والاختبار الناجح.
