# `P1-01` — جرد العقد العام ومنع تسرب `internal`

- **التاريخ:** 2026-11-14
- **الوحدة:** `github.com/osmai4603-collab/accounting_engine`
- **Go:** `1.26.3` (من `go.mod`: `go 1.26.0`, `toolchain go1.26.3`)
- **الأداة:** `go list`, `go doc`, `scripts/internalapi`, `scripts/apisnapshot`
- **المرجع المعماري:** `docs/adr/ADR-0001-public-entry-point.md`, `docs/adr/ADR-0007-internal-import-boundaries.md`

## 1. تصنيف الحزم

| المستوى | الحزم | الجمهور | الثبات | استراتيجية التوافق |
| --- | --- | --- | --- | --- |
| عقد API مستقر | `pkg/api/v1/types`, `pkg/api/v1/ledger`, `pkg/api/v1/reporting`, `pkg/api/v1/events` | كل المستهلكين والموائمات | مستقر داخل `v1` | SemVer؛ لا حذف داخل `v1`؛ إضافة `v2` عند الكسر |
| عقد SPI مستقر | `pkg/spi/storage`, `pkg/spi/masterdata`, `pkg/spi/application`, `pkg/spi/events`, `pkg/spi/standards` | مطورو الموائمات والمضيفون | مستقر | SemVer؛ اختبار تجميع خارجي؛ ضمانات موثقة |
| نقطة الدخول الموصى بها | `pkg/sdk` | التطبيقات | مستقر | SemVer؛ تغليف رقيق فوق Ports |
| قوالب تكامل | `pkg/sdk/templates` | تكامل قطاعي اختياري | تجريبي | قد يتغير في MINOR مع إشعار |
| موائمات مرجعية | `storage/memory`, `storage/postgres` | اختبارات وتطوير ونشر مرجعي | مستقر جزئيًا | السطح المدعوم: `New`, `Ports`, `MasterDataStore`, `Reporting`, `Governance`, `Close` |
| توافق مهمَل | الجذر `accounting_engine`, `pkg/spi/legacy` | مستهلكو ما قبل `v0.0.0` | مهمَل | إزالة في أول MAJOR؛ البديل موثق |
| داخلي | `internal/**`, `internal/legacyengine`, `internal/adapters/**` | داخل الوحدة فقط | غير عام | غير قابل للاستيراد خارجيًا بحكم Go |

## 2. جرد الرموز المصدَّرة

الأعداد من `go doc -all` (كل سطر `type`/`func`/`var`/`const`)، والجرد الكامل الآلي في
`docs/api/v1.snapshot` (968 سطرًا يشمل الحقول والتواقيع).

| الحزمة | عدد الرموز المصدَّرة | الغرض | المالك | ملاحظات التوافق |
| --- | ---: | --- | --- | --- |
| `.` (الجذر) | 25 | Facade مهمَل: مرادفات `pkg/sdk` فقط | فريق المنصة | لا تنفيذ؛ يزال في MAJOR القادم |
| `pkg/api/v1/events` | 11 | حدث محاسبي، قاعدة ترحيل، Router | فريق النطاق | DTO عام؛ لا أنواع داخلية |
| `pkg/api/v1/ledger` | 20 | قيد وسطر وحالة وتصنيف وفترة | فريق النطاق | DTO عام؛ الحقول الجديدة إضافية |
| `pkg/api/v1/reporting` | 10 | تقارير ميزان/دخل/ميزانية/دفتر | فريق التقارير | DTO عام |
| `pkg/api/v1/types` | 59 | معرفات، أموال، عملات، أخطاء | فريق النطاق | أساس كل العقود؛ أي كسر MAJOR |
| `pkg/sdk` | 19 | Builder وEngine وخدمات Use Case | فريق المنصة | المدخل الموصى به الوحيد |
| `pkg/sdk/templates` | 43 | قوالب قطاعية (تجارة/زراعة/SaaS) | فريق الحلول | تجريبي؛ لا يفرض قواعد محاسبية |
| `pkg/spi/application` | 20 | Principal/Authorizer/Commands/Queries/Errors | فريق المنصة | لا يتضمن تنفيذًا |
| `pkg/spi/events` | 5 | Idempotency وMapping للتكامل | فريق التكامل | Capabilityاختيارية |
| `pkg/spi/legacy` | 3 | عقود ما قبل Ports | فريق المنصة | مهمَل صراحةً؛ ليس Storage SPI |
| `pkg/spi/masterdata` | 21 | Store شركة/حساب/دفتر/سنة/فترة | فريق البيانات الرئيسية | قراءة وكتابة منفصلتان عن الترحيل |
| `pkg/spi/standards` | 3 | StandardModule وجدولة | فريق المعايير | وحدة اختيارية |
| `pkg/spi/storage` | 23 | Ports مستقلة + Capabilities | فريق المنصة | لا Post/Reverse/إنشاء Application |
| `storage/memory` | 7 | موائم مرجعي في الذاكرة | فريق المنصة | اختبار/تطوير فقط؛ ليس إنتاجيًا |
| `storage/postgres` | 8 | موائم PostgreSQL مرجعي | فريق المنصة | `New` يطبّق الهجرات؛ لا `pgx` في السطح العام |

## 3. فصل DTO عن Domain

- `pkg/api/v1/*` لا يستورد `internal/**` (يفرضه `check_public_api_purity.sh`).
- `pkg/spi/*` لا يستورد `internal/**`.
- التحويل بين DTO العام وDomain الداخلي يتم حصريًا في
  `internal/portbridge/convert.go` مع خيارات صريحة (`GenerateMissingIDs` فقط عند
  الحاجة، ورفض التحويل الضمني للعملة أو التاريخ).
- لا تُصدَّر `pgx.Tx`, `sql.Row`, `*pgxpool.Pool`, أسماء جداول، أو SQL من أي حزمة عامة.
- `storage/postgres.Adapter` لا يعرض `Pool()` أو `NewWithPool`؛ الوصول إلى pgx
  محصور في `internal/adapters/postgres`.

## 4. الفحص الآلي

`scripts/check_public_api_purity.sh`:

1. يمنع استيراد `internal` في `pkg/api/**` و`pkg/spi/**`.
2. يشغّل `scripts/internalapi` على الجذر و`pkg/**` و`storage/**` ليرفض أي نوع
   داخلي في رمز مصدَّر (حقول/توقيعات/واجهات/أنواع مركبة).
3. `--self-test` يبني وحدة مؤقتة ويحقن مخالفتين (import داخلي، ونوع داخلي في
   توقيع مصدَّر) ويتأكد من اكتشافهما.
4. `scripts/check_api_compat.sh` يقارن `docs/api/v1.snapshot` بجرد حالي ويفشل عند
   أي حذف أو تغيير توقيع غير معلن.

## 5. الانحرافات المعروفة

- `pkg/sdk/templates` تجريبي: يضيف واجهات قطاعية قد تتغير في MINOR. لا يُعد عقدًا
  محاسبيًا ولا يفرض قواعد ترحيل.
- `pkg/spi/masterdata.Store` يجمع قراءة وكتابة البيانات الرئيسية. الفصل الكامل بين
  Provisioning وPosting dependency ممثّل في `storage.MasterDataReader`، لكن إبقاء
  `Store` يخدم أدوات الإعداد ويخضع للمراجعة في P2.
- الجذر ما زال موجودًا كـ Facade مهمَل حتى MAJOR التالي وفق `ADR-0001`؛ لا منطق
  فيه ولا أنواع داخلية.
