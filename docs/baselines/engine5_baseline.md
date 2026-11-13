# خط أساس النسخة engine5 (مُعاد قياسه)

- **التاريخ:** 2026-11-14
- **المصدر:** الشجرة الحالية (commit `4ee723e` + ملفات خطة/توثيق غير ملتزم بها).
- **البيئة:** Linux x86_64، PostgreSQL `18.6` محلي على `/var/run/postgresql`.
- **Toolchain المحلي:** Go `1.26.3` — مطابق لـ `go 1.26.0` و`toolchain go1.26.3`.
- **قاعدة الاختبار:** `accounting_test` (معزولة بالـ schema لكل اختبار)، وقاعدة منفصلة `accounting_integration_baseline` لاختبار الخدمة.
- **إصدار PostgreSQL:** 18.6؛ 122 جدولًا، 107 سياسة RLS، 36 trigger غير داخلي.

## نتائج الأوامر (2026-11-14)

| الأمر | النتيجة | رمز الخروج | التفسير |
| --- | --- | ---: | --- |
| `go version` | `go1.26.3 linux/amd64` | 0 | مطابق للمطلوب |
| `go list -m` | `accounting_engine` | 0 | المسار القديم؛ يُستبدل في P1-06 |
| `bash scripts/check_gofmt.sh` | `gofmt: clean` | 0 | تنسيق سليم |
| `go vet ./...` | لا مخرجات | 0 | سليم |
| `go test ./... -count=1` | 33 حزمة ناجحة، 0 فاشلة | 0 | نجاح كامل على Memory وPostgreSQL |
| `bash scripts/check_public_api_purity.sh` | `SUCCESS ... zero internal imports` | 0 | **مضلل**: يفحص `./pkg/...` فقط ولا يفحص Root، و`pkg/sdk` يستورد `internal` فعليًا |
| `bash scripts/check_public_api_purity.sh --self-test` | `purity matcher tests: passed` | 0 | مطابق النمط سليم |
| `bash scripts/check_coverage.sh` | `total coverage: 57.2%` | 0 | معلومة فقط، ليست دليلًا محاسبيًا |
| `bash scripts/scan_secrets.sh` | `secret scan: clean` | 0 | سليم وفق القواعد الحالية |
| `DATABASE_DSN=<fresh db> bash scripts/check_service_integration.sh` | `readiness, HTTP, and gRPC checks passed` | 0 | نجح على قاعدة جديدة |
| نفس الأمر على `accounting_test` | `applied migration ... checksum differs from source` | 1 | **فشل بيئي**: قاعدة قديمة بمخطط سابق؛ ليس فشل كود |

## تفصيل الاختبارات

- `storage/postgres`: 138.6s — اختبارات ACID والتزامن والـ migrations وRLS وTriggers على PostgreSQL فعلية.
- `pkg/sdk`: 0.64s — عقود المستهلك وEvent ingestion.
- `internal/posting`, `internal/reporting`, `internal/governance`, `storage/memory`: نجاح.
- حزم بلا اختبارات: `pkg/api/v1/{events,ledger,reporting}`, `pkg/spi/{events,masterdata,storage}`, `proto/accounting/v1`, `storage/masterdatacontract`, `internal/events`, `internal/reconciliation`, `test/service_integration`.

## ما تم إثباته

1. الشجرة تبني وتجتاز جميع اختباراتها الحالية على Go 1.26.3 وPostgreSQL 18.6.
2. اختبارات PostgreSQL تعمل فعلًا ولا تُتجاوز بصمت.
3. فحص نقاء API الحالي **لا يثبت** نقاء العقود العامة (قصور في النطاق).
4. اختبار الخدمة يحتاج قاعدة نظيفة أو migrations متوافقة؛ الفشل على قاعدة قديمة متوقع وليس عيب كود.

## ما لم يُثبت

- تغطية 57.2% لا تعني صحة محاسبية أو سلوكًا صحيحًا في الحالات غير المغطاة.
- لا توجد حتى الآن Golden Accounting Scenarios معتمدة من محاسب مستقل (مهمة P4-08).
- لا يوجد Adapter خارجي مستقل حقيقي (P3-05).
- لا توجد اختبارات تكافؤ عبر PostgreSQL/Memory/Adapter خارجي (P3-07).

## أوامر إعادة الإنتاج

```bash
go version
bash scripts/check_gofmt.sh
go vet ./...
go test ./... -count=1
bash scripts/scan_secrets.sh
bash scripts/check_public_api_purity.sh
bash scripts/check_coverage.sh
psql "postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql" -tAc 'select version()'
createdb accounting_integration_baseline
DATABASE_DSN="postgres://osm@localhost:5432/accounting_integration_baseline?sslmode=disable&host=/var/run/postgresql" bash scripts/check_service_integration.sh
```
