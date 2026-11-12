# تصنيف الأخطاء والمراقبة الأساسية

- **الحالة:** معتمد
- **التاريخ:** 2026-11-12
- **المالك:** `LEDGER` + `SRE`
- **المهمة:** `P0-T6`
- **المرجع البرمجي:** `internal/apperrors`, `internal/posting/error_classification.go`, `internal/observability`

## 1. لماذا تصنيف موحد؟

الأخطاء المالية الحساسة تُقرأ من ثلاث جهات: السجل (logging)، المقاييس (metrics)، وواجهة التشغيل (API/alerting). إذا كان كل مسار يعرّف خطأه كنص حر، يصبح من المستحيل:

- التمييز بين خطأ مدخلات (لا يحتاج تدخلاً) وعطل بنية تحتية (يحتاج تدخلاً فورياً).
- بناء تنبيهات دقيقة.
- إثبات أن خطأً ما عولج في الاختبارات.

لذلك كل خطأ مالي حرج يحمل **رمزاً ثابتاً** و**فئة واحدة** من خمس فئات مغلقة.

## 2. الفئات (Categories)

| الفئة | المعنى | أمثلة | سلوك التشغيل |
| --- | --- | --- | --- |
| `validation` | مدخلات تخالف قاعدة مجال | قيد غير متوازن، مبلغ صفري، عملة غير متطابقة | لا تنبيه؛ إعادة للعميل |
| `conflict` | تصادم مع حالة/تزامن/مورد موجود | فترة مقفلة، قيد مرحّل مسبقاً، عكس مكرر، تعارض نسخة | لا تنبيه؛ قد يُعاد الطلب |
| `authorization` | الفاعل غير مخوّل | اعتماد مطلوب، شركة غير نشطة | تنبيه أمني منخفض |
| `infrastructure` | تبعية فشلت | قاعدة بيانات، شبكة، سياق ملغى، مهلة | تنبيه فوري + SLO |
| `invariant` | حالة يجب ألا تحدث؛ رفض مغلق | خرق ذرّية، خطأ غير مصنّف | تنبيه عالٍ + صفحة |

> **قاعدة الفشل المغلق:** أي خطأ غير مصنّف يُعامل `infrastructure`/`invariant`، لا `validation`. لا نسمح لخطأ مجهول أن يمر كضجيج منخفض الخطورة.

## 3. الرموز الثابتة (Codes)

الرموز جزء من العقد التشغيلي: تُضاف ولا تُعاد تسميتها أو تُعاد استخدامها.

| الرمز | الفئة | المعنى |
| --- | --- | --- |
| `VALIDATION_FAILED` | validation | فشل تحقق عام |
| `ENTRY_INVALID` | validation | قيد/سطر غير صالح |
| `ENTRY_UNBALANCED` | validation | مجموع مدين ≠ مجموع دائن |
| `PERIOD_NOT_FOUND` | validation | لا توجد فترة مطابقة |
| `PERIOD_CLOSED` | conflict | الفترة مقفلة (ناعم/نهائي) |
| `PERIOD_TRANSITION_INVALID` | conflict | انتقال حالة فترة غير مسموح |
| `APPROVAL_REQUIRED` | authorization | يلزم اعتماد مكتمل |
| `APPROVAL_POLICY_UNCONFIGURED` | authorization | سياسة الشركة غير مهيأة (Fail-Closed) |
| `CONFLICT` | conflict | تصادم حالة عام |
| `ENTRY_VERSION_CONFLICT` | conflict | تحكم تفاؤلي/نسخة متوقعة |
| `ENTRY_ALREADY_POSTED` | conflict | القيد مرحّل مسبقاً |
| `ENTRY_IMMUTABLE` | conflict | محاولة تعديل حقل مؤثر بعد الاعتماد |
| `ATOMICITY_VIOLATION` | invariant | خرق عقد الذرّية/المعاملة |
| `TENANT_SCOPE_VIOLATION` | authorization | محاولة وصول عبر الشركات |
| `MIGRATION_INCOMPATIBLE` | invariant | هجرة غير متوافقة |
| `INFRASTRUCTURE_FAILURE` | infrastructure | فشل تبعية/مهلة/إلغاء سياق |
| `UNKNOWN_ERROR` | invariant | خطأ غير مصنّف |

## 4. خريطة الأخطاء الحرجة

| الخطأ المصدر | الرمز | الفئة |
| --- | --- | --- |
| `financialtypes.ErrFiscalPeriodClosed` | `PERIOD_CLOSED` | conflict |
| `policy.ErrHardClosedPeriod`, `policy.ErrSoftClosedPeriod` | `PERIOD_CLOSED` | conflict |
| `financialtypes.ErrInvalidPeriodTransition` | `PERIOD_TRANSITION_INVALID` | conflict |
| `financialtypes.ErrFiscalPeriodNotFound`, `masterdata.ErrFiscalPeriodNotFound` | `PERIOD_NOT_FOUND` | validation |
| `posting.ErrApprovalRequired` | `APPROVAL_REQUIRED` | authorization |
| `masterdata.ErrCompanyInactive` | `VALIDATION_FAILED` | authorization |
| `financialtypes.ErrUnbalancedMove`, `ErrFunctionalBalanceMismatch`, `ErrZeroAmountLine`, `ErrInvalidAmount`, `ErrCurrencyMismatch` | `ENTRY_UNBALANCED` | validation |
| `financialtypes.ErrMoveAlreadyPosted`, `posting.ErrEntryAlreadyReversed`, `ErrCannotModifyPostedMove`, `ErrEntryNotDraft` | `CONFLICT` | conflict |
| `posting.ErrAtomicPostingOpenLineRequired`, `ErrUnsupportedMutationBackend` | `ATOMICITY_VIOLATION` | invariant |
| `context.Canceled`, `context.DeadlineExceeded` | `INFRASTRUCTURE_FAILURE` | infrastructure |
| أي خطأ آخر | `UNKNOWN_ERROR` | invariant |

الكود المرجعي: `internal/posting/error_classification.go::ClassifyError`.

## 5. السجلات (Logging)

كل خطأ مالي حرج يُسجَّل بحقول الارتباط التالية (تُضاف تلقائياً عبر `observability.Handler`/`Unit.Enrich` عند وجودها في السياق):

| الحقل | المصدر |
| --- | --- |
| `company_id` | `observability.WithCompanyID` |
| `entry_id` | `observability.WithEntryID` |
| `period_id` | `observability.WithPeriodID` |
| `trace_id` / `traceparent` | `observability.ContextWithTraceparent` |
| `error_code` | `apperrors.CodeOf` / `ClassifyError` |
| `error_category` | `ClassifyError` |

مثال سجل:

```json
{"level":"ERROR","msg":"post entry","company_id":"…","entry_id":"…","period_id":"…","trace_id":"…","error_code":"PERIOD_CLOSED","error_category":"conflict"}
```

## 6. المقاييس (Metrics)

تظهر في `/metrics` (Prometheus text format):

| المقياس | النوع | المعنى |
| --- | --- | --- |
| `accounting_posting_attempts_total` | counter | عدد محاولات الترحيل |
| `accounting_posted_entries_total` | counter | القيود الناجحة |
| `accounting_posting_failures_total` | counter | كل محاولات الفشل |
| `accounting_posting_failures_by_category_total{category=...}` | counter | الفشل حسب الفئة الخمس |
| `accounting_posting_duration_seconds` | histogram | زمن الترحيل |
| `accounting_transaction_duration_seconds` | histogram | زمن المعاملة الكاملة |
| `accounting_maker_checker_pending_total` | gauge | قيود بانتظار الاعتماد |
| `accounting_reconciliation_discrepancy_count` | gauge | حسابات متأخرة عن مطابقة الأرصدة |

## 7. النجاح والفشل

- محاولة الترحيل تُسجَّل في `posting_attempts_total` **قبل** معرفة النتيجة.
- الفشل يُسجَّل مرة واحدة حسب الفئة، مع `error_code` في السجل.
- لا يُسجَّل الفشل كنجاح إذا كان المسار الاحتياطي قد رحّل جزئياً؛ هذا `invariant` ويجب أن يظهر كفشل.

## 8. اختبارات العقد

- `internal/apperrors/errors_test.go` — ثبات الرموز والفئات والفشل المغلق.
- `internal/observability/telemetry_test.go` — حقن `period_id`، العدادات، exposition.
- `internal/posting` — تصنيف الأخطاء أثناء الترحيل.
