# `ARD-P1-05` — معالجة Root API وإزالة ازدواجية نقطة الدخول

- **المهمة:** `P1-05`
- **المرحلة:** `P1`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة

## 1. كيف تم التنفيذ

- نقل التنفيذ القديم كاملًا (`engine.go`, `engine_observability.go` واختباراتها)
  إلى `internal/legacyengine` لخدمة `cmd/agy` والاختبارات الداخلية.
- إعادة كتابة الجذر كـ Facade مهمَل في `facade.go`:
  - مرادفات `sdk.Engine`, `sdk.LedgerService`, `sdk.Option`, `sdk.EngineBuilder`...
  - أغلفة رقيقة `NewBuilder`, `WithStoragePorts`, `WithCoreStorage`, `WithReporting`,
    `WithGovernance`, `WithStandard`.
  - لا تنفيذ ولا حالة ولا استيراد لـ`internal`.
- تحديث `doc.go` بإشعار إهمال صريح ومسار هجرة.
- كتابة `docs/migration/root-facade.md` بقائمة ما تغيّر وما أُزيل.
- تحديث كل مستهلكي الجذر الداخليين (`cmd/agy`, اختبارات الموائمات) إلى
  `internal/legacyengine`.
- توسيع فحص النقاء ليفحص الجذر و`pkg/**` و`storage/**`.

## 2. إثبات معايير القبول

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| مدخل عام موصى به واحد موثق | `pkg/sdk` + `docs/public_api_inventory.md` + `docs/migration/root-facade.md` | محقق |
| لا يكشف نوع `internal/*` في الحقول أو التوقيعات | `internalapi` على الجذر = صفر مخالفات؛ `go doc -all .` يعرض مرادفات `sdk` فقط | محقق |
| تغييرات التوافق موثقة | `ADR-0001`, `docs/migration/root-facade.md` | محقق |
| أمثلة المستهلك لا تستورد Root القديم | `test/external_consumer` و`pkg/sdk` أمثلتها على `WithStoragePorts` | محقق |

## 3. النواقص

- الجذر يبقى حتى MAJOR القادم وفق `ADR-0001`؛ لا يمكن حذفه الآن دون كسر الفترة
  الانتقالية.
- `internal/legacyengine` يحتفظ بـ`Config` ووحدات المعايير الغنية؛ ليست عامة.

## 4. ملاحظات

- إزالة `New` من الجذر كسر مقصود لما قبل `v0.0.0`؛ البديل موثق ومختبر.
- `pkg/sdk` لا يستورد الجذر، والجذر يستورد `pkg/sdk` فقط.
