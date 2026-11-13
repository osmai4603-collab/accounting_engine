# `ARD-P1-03` — تنفيذ Public SPI المستقل

- **المهمة:** `P1-03`
- **المرحلة:** `P1`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة

## 1. كيف تم التنفيذ

- تنفيذ العقود العامة في `pkg/spi/storage` و`pkg/spi/application` و
  `pkg/spi/events` و`pkg/spi/masterdata` و`pkg/spi/standards`.
- إضافة محولات داخلية صريحة في `internal/portbridge`:
  - `Repository` ينفذ `posting.Repository` فوق Ports؛
  - `MasterDataAdapter` ينفذ `masterdata.ContextStorageRepository` قراءةً؛
  - `ToInternalEntry`/`ToPublicEntry` تحويلات صريحة ترفض نقص الهوية/العملة/التاريخ.
- جعل `storage/memory` و`storage/postgres` يعرضان `Ports()` فقط في سطحهما العام،
  ونقل التنفيذ الكامل إلى `internal/adapters/{memory,postgres}`.
- إضافة تأكيدات واجهات وقت الترجمة في المحولات والموائمات.
- توثيق الضمانات (ذرية الترحيل، العزل، التفرد، ترتيب الأقفال، معاني الأخطاء) في
  `docs/public_contracts_design.md`.

## 2. إثبات معايير القبول

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| مشروع Go خارجي ينفذ SPI دون `internal` | `test/external_consumer/ports_adapter_test.go` ينفذ كل Ports ويُرحّل قيدًا بنجاح | محقق |
| العقود لا تتطلب `pgx`/`sql.Tx`/أسماء جداول | `go doc` و`internalapi` ومراجعة التوقيعات | محقق |
| ضمانات كل Port موثقة وقابلة للاختبار | جدول الضمانات + `ports_test.go` + `convert_test.go` | محقق |
| الأخطاء العامة قابلة للفحص عبر `errors.Is`/أنواع موثقة | `pkg/spi/application/errors_test.go` يثبت `errors.Is` و`errors.As` وإخفاء `Cause` | محقق |

## 3. النواقص

- `storage.BalanceReader` و`AuditWriter` و`IdempotencyStore` معرّفة لكن موائمات
  الذاكرة/PostgreSQL لم توصّلها بعد في `Ports()`؛ تُغلق في `P2-06`/`P3`.
- `TransactionRunner` معرّف نظريًا؛ لا يستخدمه `portbridge` بعد لأن الترحيل
  يستخدم عمليات الموائم الذرية مباشرة.

## 4. ملاحظات

- اختبار التحويل يثبت أن الهوية والتواريخ والعملة وسعر الصرف والتسلسل والرقم
  القانوني لا تتغير، وأن توليد IDs لا يحدث إلا بطلب صريح.
- لا توجد أي `nil` capability تعمل كأنها مفعّلة؛ الغياب يُرجع خطأً صريحًا.
