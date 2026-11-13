# `ARD-P1-04` — إعادة بناء SDK Builder على العقود العامة

- **المهمة:** `P1-04`
- **المرحلة:** `P1`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة

## 1. كيف تم التنفيذ

- إضافة `sdk.WithStoragePorts(storage.Ports)` كمسار التكوين الموصى به.
- إبقاء `sdk.WithCoreStorage` مهمَلًا ومحصورًا في مسار توافق MAJOR القادم.
- `EngineBuilder.Build` أصبح على مرحلتين:
  1. `preflight()` يجمع أخطاء الخيارات، تعارض مصدر التخزين، كل Port مفقود،
     تكرار/اعتماديات وحدات المعايير، ويرفض الوحدات مع Ports بخطأ صريح.
  2. لا يبدأ `Initialize` أو `RegisterRules` إلا بعد نجاح preflight بالكامل.
- بناء Application Layer عبر `sdkapplication.NewApplicationFromPorts` بدل طلب
  `NewLedgerApplication()` من الموائم.
- إزالة Type Assertion المخفي لعقد MasterData من مسار Ports؛ العقد صريح.
- Reporting/Governance اختياريان: `WithReporting(nil)`/`WithGovernance(nil)` يرفضان،
  وغيابهما يُرجع خطأ "not configured" عند الاستخدام بدل تنفيذ صامت.
- تجميع أخطاء Build مع تسمية Capability/الاعتمادية الناقصة.
- اختبارات:
  - `TestBuilder_PortsOnlyPostingJourney` (ترحيل فعلي من Ports)؛
  - `TestBuilder_RejectsBothStorageSources`؛
  - `TestBuilder_PortsPreflightReportsEveryMissingPort`؛
  - `TestBuilder_PreflightRunsBeforeModuleInitialize`؛
  - `TestBuilder_PortsModeRejectsStandardsExplicitly`؛
  - `TestBuilder_OptionalCapabilitiesFailExplicitly`.

## 2. إثبات معايير القبول

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| Builder لا يستدعي `NewLedgerApplication()` من التخزين | مسار Ports يستخدم `NewApplicationFromPorts`؛ لا استدعاء Provider | محقق |
| Adapter خارجي يُركَّب من Public Ports فقط | `test/external_consumer/ports_adapter_test.go` | محقق |
| لا يبدأ Initialize قبل Preflight كامل | اختبار عدّاد `initializeCalls == 0` عند فشل preflight | محقق |
| Core يعمل دون Reporting/Governance | `TestBuilder_OptionalCapabilitiesFailExplicitly` يبني وينجح | محقق |
| غياب Dependency مطلوب ينتج خطأ واضحًا | اختبار يجمع أسماء كل Port ناقص في رسالة واحدة | محقق |

## 3. النواقص

- Standards تتطلب `WithCoreStorage` في هذا الإصدار لأن `StandardModule` يعتمد
  `legacy.CoreLedgerStore`؛ يُعالج في `P3-02`.
- Event ingestion في وضع Ports يدعم المسودة الجاهزة فقط؛ بناء القواعد يحتاج
  MasterDataReader موسّعًا وIdempotency Port (P2-06).
- `sdk.LedgerService` ما زال بالشكل القديم؛ أوامر `pkg/spi/application` تُوصل في P2.

## 4. ملاحظات

- رسائل الفشل تذكر Capability والاعتمادية، مثل:
  `storage ports: storage: posting writer is required`.
- اختبار الترحيل من Ports يثبت أن `Ports.Validate` + `portbridge` + Posting Engine
  تعمل دون أي معرفة من الموائم بطبقة التطبيق.
