# ARD-P6-01: Fail-Closed Production Configuration and Security

- **المهمة:** `P6-01`
- **المرحلة:** `P6`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة
- **الملفات المعنية:** `cmd/accounting-service/config.go`، `cmd/accounting-service/main.go`، `cmd/accounting-service/config_test.go`

## 1. السياق والمشكلة (Context & Problem)

كانت دالة `LoadConfig()` تضبط `DATABASE_DSN` تلقائيًا إلى `memory` عند غياب المتغير في البيئة، دون اشتراط تحديد صريح للبيئة (`APP_ENV`)، مما يسمح بتشغيل الخدمة المالية المحاسبية بذاكرة مؤقتة (In-Memory) في الإنتاج دون وعي من المشغل، بالإضافة إلى غياب التحقق من صحة عناوين قواعد البيانات وعدم حجب كلمات المرور والأسرار من سجلات التشغيل ورسائل الخطأ.

المطلوب:
1. جعل التهيئة في الإنتاج `Fail-Closed`: الفشل الإلزامي عند غياب إعداد التخزين الدائم.
2. منع استخدام التخزين المؤقت `memory` في الإنتاج منعًا باتًا.
3. اشتراط الاختيار الصريح لـ `memory` في بيئات التطوير والاختبار (`development`/`test`).
4. حجب الأسرار وكلمات المرور (`SanitizeDSN`) من كافة السجلات ورسائل الأخطاء.
5. التحقق من صحة المنافذ وإعدادات مجمع الاتصالات (Connection Pool).

## 2. القرارات والتنفيذ (Decisions & Implementation)

1. **البيئات المعتمدة (`Environment`):**
   - تم تعريف نوع `Environment` مع الثوابت `production` و`development` و`test`.
   - تدعم دالة `ParseEnvironment` أسماء مرادفة (`prod`، `dev`، `local`، `testing`).
   - أي بيئة غير معتمدة ترفض فورًا بخطأ `ErrInvalidEnvironment`.

2. **التحقق الصارم وإغلاق الثغرات (`Fail-Closed Validation`):**
   - في بيئة `production`:
     - إذا لم يُضبط `DATABASE_DSN`، تفشل الخدمة بخطأ واضح: `ErrProductionMissingDSN`.
     - إذا حُدد `memory` أو ضُبط `ALLOW_IN_MEMORY=true`، تفشل الخدمة بخطأ `ErrProductionMemoryStore`.
     - يجب أن يبدأ عنوان قاعدة البيانات بـ `postgres://` أو `postgresql://`، وإلا يُرفض بخطأ `ErrInvalidPostgresDSN`.
   - في بيئتي `development` و`test`:
     - لا يُسمح بالتحول التلقائي الخفي إلى `memory`.
     - إذا تُرك `DATABASE_DSN` فارغًا دون تفعيل صريح لـ `ALLOW_IN_MEMORY=true`، تفشل الخدمة بخطأ `ErrUnconfiguredStorage`.
     - التخزين المؤقت مسموح فقط باختيار صريح (`DATABASE_DSN=memory` أو `ALLOW_IN_MEMORY=true`).

3. **حجب الأسرار (`SanitizeDSN`):**
   - دالة `SanitizeDSN` تعالج عناوين URL القياسية ومفاتيح الاتصال `libpq` (key-value strings).
   - تستبدل كلمات المرور بـ `REDACTED` مع إبقاء المضيف والميناء واسم قاعدة البيانات للتشخيص.
   - رسائل الأخطاء وسجلات التشغيل في `main.go` و`config.go` تمر دائمًا عبر `SanitizeDSN`.

4. **إعدادات المنافذ ومجمع الاتصال:**
   - فحص صحة `HTTPPort` و`GRPCPort` (أرقام صحيحة بين 1 و65535).
   - خيارات `DBMaxConns`، `DBMinConns`، `RequireTLS`.

## 3. إثبات معايير القبول (Verification Evidence)

تم اختبار كافة الحالات في `cmd/accounting-service/config_test.go` بنجاح 100%:

| معيار القبول | الدليل الاختباري | النتيجة |
| --- | --- | --- |
| `production` دون DSN يفشل برسالة واضحة | `TestProductionMissingDSNFailsClosed` | محقق (`ErrProductionMissingDSN`) |
| لا تبدأ `production` على Memory افتراضيًا أو صراحة | `TestProductionMemoryProhibited` و `TestProductionMemoryViaFlagProhibited` | محقق (`ErrProductionMemoryStore`) |
| `development`/`test` يسمحان بـ Memory باختيار صريح فقط | `TestDevelopmentExplicitMemoryAllowed` و `TestDevelopmentAllowInMemoryFlagAllowed` و `TestTestEnvironmentExplicitMemoryAllowed` | محقق |
| عدم السقوط الصامت إلى Memory في `development` | `TestDevelopmentUnsetDSNFailsClosed` | محقق (`ErrUnconfiguredStorage`) |
| عدم ظهور الأسرار في السجلات أو الأخطاء | `TestSanitizeDSNRedactsSecrets` (URL + Key-Value formats) | محقق (حجب كلمات المرور بالكامل) |
| التحقق من المنافذ والبيئة | `TestInvalidEnvironmentFails` و `TestInvalidPortConfigurations` | محقق |
| تكامل خادم HTTP وgRPC | `go test -v ./cmd/accounting-service` (21/21 tests pass) | محقق |

## 4. الحدود (Limits)

- تغطي هذه المهمة إعدادات خدمة `cmd/accounting-service` والتأكد من أمان بيئات النشر وتخزين PostgreSQL.
- إدارة الهوية والمصادقة في الـ Handlers تتبع المهمة اللاحقة `P6-02`.

