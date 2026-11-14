# ARD-P6-04: إعادة تصميم عقود gRPC واستكمال تنفيذها

- **المهمة:** `P6-04`
- **المرحلة:** `P6`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة
- **الملفات المعنية:** `proto/accounting/v1/*.proto`, `proto/accounting/v1/*.pb.go`, `proto/accounting/v1/*_grpc.pb.go`, `cmd/accounting-service/grpc_server.go`, `cmd/accounting-service/grpc_server_test.go`

## 1. السياق والمشكلة (Context & Problem)

كانت عقود gRPC السابقة تعاني من قصور يعيق استخدامها كحدود خدمة مكتملة:
1. `PostEntryRequest` في `ledger.proto` كان يحتوي فقط على `company_id` و`description` دون تفاصيل السطور أو الدفتر أو الفترة أو المبالغ، وكان المعالج يعيد خطأ `InvalidArgument` لإخفاق الطلب غير المكتمل.
2. استجابات التقارير المالية `GetBalanceSheetResponse` و`GetIncomeStatementResponse` في `reporting.proto` كانت تعيد `company_id` و`bool success` فقط، دون إرجاع بيانات التقرير أو الأصول والالتزامات والإيرادات والمصروفات.
3. غياب RPC لعكس القيود المحاسبية (`ReverseEntry`) في خدمات gRPC.
4. عدم وجود تحويل دلالي شامل لأخطاء المجال (Domain Errors) إلى رموز حالات gRPC (`codes.InvalidArgument`, `codes.NotFound`, `codes.PermissionDenied`, `codes.AlreadyExists`, `codes.Unavailable`, etc.).

المطلوب:
1. تصميم بروتوكول gRPC يعكس حالات الاستخدام الفعلية للمحرك المحاسبي.
2. تمكين إنشاء وتأكيد القيود كاملة السطور (`PostEntry`) أو اعتماد المسودة (`draft_entry_id`).
3. توفير خدمة عكس القيود `ReverseEntry`.
4. إعادة بيانات التقرير كاملة في استجابات `GetTrialBalance` و`GetBalanceSheet` و`GetIncomeStatement`.
5. ربط اعتراض المصادقة (`P6-02`) وتوفير اختبارات تكاملية وشاملة لـ gRPC Client & Server.

## 2. القرارات والتنفيذ (Decisions & Implementation)

1. **تحديث بروتوكول `ledger.proto`:**
   - إضافة رسالة `EntryLineProto` (تحوي `line_id`, `account_id`, `partner_id`, `cost_center_id`, `project_id`, `transaction_amount`, `transaction_currency`, `exchange_rate`, `functional_debit`, `functional_credit`).
   - توسيع `PostEntryRequest` ليشمل `journal_id`, `fiscal_period_id`, `effective_date`, `source_module`, `reference_id`, `transaction_currency`, `exchange_rate`, `lines`, و`draft_entry_id`.
   - إضافة `ReverseEntry` RPC ورسائل `ReverseEntryRequest` و`ReverseEntryResponse`.
   - توسيع `GetEntryResponse` ليشمل السطور الكاملة وتفاصيل القيد.
   - إضافة `TrialBalanceLineProto` وتوسيع `GetTrialBalanceResponse` ليشمل `lines`, `total_debit`, `total_credit`, `is_balanced`.

2. **تحديث بروتوكول `reporting.proto`:**
   - إضافة `ReportLineProto` و`ReportSectionProto`.
   - توسيع `GetBalanceSheetResponse` ليشمل `is_balanced`, `total_assets`, `total_liabilities_and_equity`, والأقسام التفصيلية للأصول والالتزامات وحقوق الملكية.
   - توسيع `GetIncomeStatementResponse` ليشمل `net_income` والأقسام التفصيلية للإيرادات والمصروفات.

3. **تحديث معالج الخدمة `grpc_server.go`:**
   - `PostEntry`: يدعم الترحيل عن طريق `draft_entry_id` أو إنشاء وترحيل قيد كامل من السطور والمبالغ المرسلة.
   - `ReverseEntry`: ينفذ العكس المحاسبي عبر المحرك ويعيد المعرف والمرفقات.
   - `GetTrialBalance` / `GetBalanceSheet` / `GetIncomeStatement`: يحول مخرجات SDK Reporting إلى الرسائل البروتوكولية المحدثة.
   - `grpcApplicationError`: يحول أخطاء التطبيق (`ErrValidation`, `ErrNotFound`, `ErrForbidden`, `ErrConflict`, `ErrDuplicateRequest`, `ErrDependencyUnavailable`) إلى رموز gRPC القياسية.

4. **تحديث وتمرير المولدات والنعكاس (Reflection):**
   - تم تحديث الملفات المولدة `*.pb.go` و`*_grpc.pb.go` بأسلوب متوافق تمامًا مع `protoc-gen-go v1.36.12` و`protoc-gen-go-grpc 1.5.1`.
   - استمرار تفعيل Server Reflection V1 على المنافذ والملفات السليمة.

## 3. إثبات معايير القبول (Verification Evidence)

تم اختبار كافة RPCs في `cmd/accounting-service/grpc_server_test.go`:

| معيار القبول | الدليل الاختباري | النتيجة |
| --- | --- | --- |
| عدم قبول PostEntry دون بيانات سطرية أو مسودة صالحة | `TestGRPCPostEntryRejectsIncompleteContract` | محقق (`codes.InvalidArgument`) |
| نجاح PostEntry حمولة كاملة السطور والمبالغ | `TestGRPCPostEntryCompletePayload` | محقق |
| نجاح ReverseEntry RPC | `TestGRPCReverseEntryRPC` | محقق |
| إرجاع بيانات ميزان المراجعة كاملة | `TestGRPCGetTrialBalanceReturnsData` | محقق (`lines`, `total_debit`, `total_credit`) |
| إرجاع بيانات الميزانية العمومية كاملة | `TestGRPCGetBalanceSheetReturnsData` | محقق (`assets`, `liabilities`, `equity`) |
| إرجاع بيانات قائمة الدخل كاملة | `TestGRPCGetIncomeStatementReturnsData` | محقق (`net_income`, `revenues`, `expenses`) |
| تحويل أخطاء المجال إلى رموز status دلالية | `TestGRPCMapsApplicationErrorCategories` | محقق |
| تطابق معالجة أحداث Ingestion بين HTTP وgRPC | `TestHTTPAndGRPCIngestionNormalizeTheSameCommand` | محقق |
| تسجيل وانعكاس خدمات gRPC عبر ServerReflection | `TestGRPCRegistersReflectsAndInvokesProtoServices` | محقق |

## 4. الحدود (Limits)

- تقتصر عقود gRPC على الواجهات المحددة في `proto/accounting/v1/`.
- حدود الأمان والتوثيق تدار عبر المعترضات المحقونة من `P6-02`.
