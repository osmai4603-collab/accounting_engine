# خط أساس النسخة engine5

- **التاريخ:** 2026-11-13
- **المصدر:** `engine5.zip`
- **البيئة:** Linux x86_64 container، الشبكة الخارجية غير متاحة، PostgreSQL DSN غير مضبوط.
- **Toolchain المطلوب من المشروع:** Go `1.26.0` مع `toolchain go1.26.3`.
- **Toolchain المحلي:** Go `1.23.2`؛ لا يفي بمتطلب `go.mod`.

## نتائج الأوامر

| الأمر | النتيجة | رمز الخروج | التفسير |
| --- | --- | ---: | --- |
| `bash scripts/check_gofmt.sh` | `gofmt: clean` | 0 | تنسيق الملفات الحالي سليم وفق الأداة المتاحة |
| `bash scripts/scan_secrets.sh` | `secret scan: clean` | 0 | لم يكتشف الفاحص أسرارًا وفق قواعده الحالية |
| `go list ./...` | فشل تنزيل `go1.26.3` من `proxy.golang.org` بسبب DNS/شبكة | 1 | لا يمكن استخراج package list عبر Go في هذه البيئة |
| `go version` | حاول تنزيل `go1.26.3` وفشل | 1 | بسبب `toolchain` directive |
| `GOTOOLCHAIN=local go version` | `go1.23.2 linux/amd64` | 0 | الإصدار المحلي أقدم من متطلبات المشروع |
| `bash scripts/check_public_api_purity.sh` | فشل عند تشغيل Go toolchain | 1 | لم يصل إلى نتيجة فحص API؛ ليس دليلًا على وجود/عدم وجود تسرب |
| `bash scripts/check_coverage.sh` | فشل عند تشغيل Go toolchain | 1 | لا توجد نتيجة تغطية جديدة |
| `bash scripts/check_service_integration.sh` | `DATABASE_DSN must point to PostgreSQL` | 1 | لا توجد قاعدة PostgreSQL اختبارية موصولة |

## ما لم يتم إثباته

- لم يتم تشغيل `go test ./...` بنجاح أو فشل؛ الأداة لا تستطيع استخدام الإصدار المطلوب.
- لم يتم تشغيل اختبارات PostgreSQL أو migrations أو ACID في هذه البيئة.
- لم يتم التحقق من إصدار PostgreSQL أو إعدادات العزل أو RLS أو نتائج الترحيل الفعلية.
- لا يجوز استنتاج أن الاختبارات فاشلة من فشل تنزيل toolchain، ولا يجوز أيضًا اعتبارها ناجحة دون تشغيلها.

## متطلبات إكمال خط الأساس

1. توفير Go `1.26.3` أو toolchain متوافق مع `go.mod` في بيئة البناء.
2. توفير PostgreSQL اختبارية مع `DATABASE_DSN` معزول وعدم استخدام قاعدة بيانات إنتاج.
3. إعادة تشغيل جميع الأوامر أعلاه، ثم `go test ./...` واختبارات التكامل المحددة في CI.
4. حفظ الإصدارات، الأوامر، رموز الخروج، والسجلات المختصرة في هذا الملف.
