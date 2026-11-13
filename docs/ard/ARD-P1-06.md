# `ARD-P1-06` — تثبيت مسار Go Module وسياسة SemVer

- **المهمة:** `P1-06`
- **المرحلة:** `P1`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة

## 1. كيف تم التنفيذ

- اعتماد المسار الرسمي `github.com/osmai4603-collab/accounting_engine` بعد موافقة
  المالك، وتحديث `go.mod` وكل الاستيرادات و`test/external_consumer/go.mod`.
- إبقاء `go 1.26.0` و`toolchain go1.26.3` دون تخفيض.
- تثبيت سياسة SemVer في `ADR-0002`: MAJOR للكسر، MINOR للإضافة، PATCH للإصلاح،
  مع تغطية Go API وSPI وProtobuf والإهمال.
- إنشاء `scripts/apisnapshot` و`scripts/check_api_compat.sh`:
  - الجرد الحالي في `docs/api/v1.snapshot` (985 سطرًا)؛
  - أي حذف أو تغيير توقيع غير معلن يفشل CI؛
  - `--update` هو التحديث المتعمد فقط.
- إنشاء `scripts/check_published_module.sh`:
  - يبني Module Proxy محليًا (`.info`/`.mod`/`.zip`) من الشجرة الحالية؛
  - ينشئ وحدة مستقلة `example.com/publishedconsumer`؛
  - يستورد الوحدة **دون `replace`** ويبني ويشغّل محركًا من `Ports`.
- إضافة `make api-compat` و`make published-module` وخطوتَي CI.

## 2. إثبات معايير القبول

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| `go.mod` يحمل المسار الرسمي | `go list -m` = `github.com/osmai4603-collab/accounting_engine` | محقق |
| مشروع مستقل يستورد دون `replace` | `./scripts/check_published_module.sh` يطبع `published module consumer: ok` | محقق |
| سياسة SemVer تغطي Go API/Protobuf/SPI | `ADR-0002` + `docs/api/v1.snapshot` | محقق |
| CI يكتشف كسر التوافق غير المعلن | خطوة `API compatibility gate` + `published-module consumer gate` في `.github/workflows/ci.yml` | محقق |

## 3. النواقص

- لا يوجد إصدار منشور فعليًا على مستودع عام بعد؛ الاختبار يستخدم Proxy محليًا
  يحاكي النشر. النشر الحقيقي جزء من `P7`.
- فحص التوافق يغطي Go API فقط؛ فحص Protobuf التوافقي يُغلق في `P7-02`.

## 4. ملاحظات

- `check_published_module.sh` يستثني `test/external_consumer` (وحدة فرعية) من
  حزمة النشر، ويستبعد `.git` و`.gotmp`.
- الجرد يحتوي حقول Struct وتواقيع الدوال والواجهات، لذا تغيير حقل عام يظهر ككسر.
