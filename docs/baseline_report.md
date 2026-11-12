# تقرير خط الأساس وإعادة إنتاج النتائج

- **الحالة:** نشطة — خط الأساس المعتمد لبوابة `G0`
- **التاريخ:** 2026-11-12
- **المالك:** `QA` + `SRE`
- **المهمة:** `P0-T1` (مع إعادة تحقق نهائية بعد `P0-T2`…`P0-T8`)
- **الالتزام الأساس:** `6183a84c4b1173eedfcb45cfd9eaf3941ed8dfa2`
- **خط CI:** [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) — شرح التشغيل في [`ci.md`](./ci.md)

## 1. هوية البيئة

| العنصر | القيمة |
| --- | --- |
| المستودع | `github.com/osmai4603-collab/accounting_engine` |
| الفرع الافتراضي | `main` |
| لغة Go | `go1.26.3` |
| نظام التشغيل / المعمارية | `linux` / `amd64` |
| PostgreSQL | `18.6` (Ubuntu 18.6-1.pgdg24.04+2) |
| برنامج تشغيل قاعدة البيانات | `pgx/v5` |
| وحدة Go | `accounting_engine` |
| تاريخ التقاط خط الأساس | 2026-11-12 |
| حالة شجرة العمل عند الالتقاط | نظيفة عند الالتزام الأساس |

أوامر التحقق من الهوية:

```bash
git rev-parse HEAD
go version
go env GOOS GOARCH GOVERSION
psql "$POSTGRES_URL" -Atc 'select version();'
```

النتائج الفعلية:

```text
6183a84c4b1173eedfcb45cfd9eaf3941ed8dfa2
go version go1.26.3 linux/amd64
linux
amd64
go1.26.3
PostgreSQL 18.6 (Ubuntu 18.6-1.pgdg24.04+2) on x86_64-pc-linux-gnu, compiled by gcc (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0, 64-bit
```

## 2. الأوامر والنتائج

| الأمر | النتيجة | ملاحظة |
| --- | --- | --- |
| `gofmt -l .` | ✅ لا ملفات | البوابة تفشل عند أي ملف غير منسق |
| `go vet ./...` | ✅ بلا تحذيرات | |
| `go build ./...` | ✅ نجح | |
| `go test -count=1 ./...` | ✅ جميع الحزم ناجحة | 23 حزمة؛ 22 ناجحة و`internal/reconciliation` بلا ملفات اختبار |
| `go test -race -count=1 ./...` | ✅ بلا سباقات | انظر §4 |
| `go test -count=1 -coverprofile=coverage.out ./...` | ✅ | التغطية الإجمالية في §5 |
| `go test -count=1 -tags gapred -run TestGAP ./...` | ❌ متوقع | يفشل عند الفجوات الحمراء فقط؛ انظر [`traceability_matrix.md`](./traceability_matrix.md) |

## 3. جدول الحزم والاختبارات

العدّ من ملفات `*_test.go`، والزمن من تشغيل `go test -count=1 ./...` على قاعدة PostgreSQL جديدة (تشغيل 2026-11-12).

| الحزمة | ملفات الاختبار | دوال `Test` | النتيجة | الزمن |
| --- | ---: | ---: | --- | ---: |
| `accounting_engine` | 7 | 19 | ✅ PASS | 1.54s |
| `accounting_engine/cmd/agy` | 2 | 4 | ✅ PASS | 0.04s |
| `accounting_engine/internal/apperrors` | 1 | 6 | ✅ PASS | 0.01s |
| `accounting_engine/internal/consolidation` | 3 | 11 | ✅ PASS | 0.02s |
| `accounting_engine/internal/governance` | 6 | 23 | ✅ PASS | 0.56s |
| `accounting_engine/internal/ias12` | 3 | 12 | ✅ PASS | 0.01s |
| `accounting_engine/internal/ifrs15` | 1 | 6 | ✅ PASS | 0.03s |
| `accounting_engine/internal/ifrs16` | 2 | 10 | ✅ PASS | 0.04s |
| `accounting_engine/internal/ifrs9` | 2 | 10 | ✅ PASS | 0.03s |
| `accounting_engine/internal/ifrsautomation` | 3 | 10 | ✅ PASS | 0.07s |
| `accounting_engine/internal/masterdata` | 3 | 12 | ✅ PASS | 0.01s |
| `accounting_engine/internal/observability` | 2 | 11 | ✅ PASS | 0.04s |
| `accounting_engine/internal/policy` | 1 | 3 | ✅ PASS | 0.01s |
| `accounting_engine/internal/posting` | 9 | 37 | ✅ PASS | 0.17s |
| `accounting_engine/internal/reconciliation` | 0 | 0 | ⚪ لا ملفات اختبار | 0.00s |
| `accounting_engine/internal/reporting` | 8 | 40 | ✅ PASS | 0.08s |
| `accounting_engine/internal/reporting/taxonomy` | 1 | 12 | ✅ PASS | 0.01s |
| `accounting_engine/internal/subledgers` | 5 | 49 | ✅ PASS | 0.02s |
| `accounting_engine/internal/subledgers/bank_parsers` | 2 | 3 | ✅ PASS | 0.01s |
| `accounting_engine/internal/types` | 2 | 13 | ✅ PASS | 0.01s |
| `accounting_engine/internal/valuation` | 2 | 7 | ✅ PASS | 0.01s |
| `accounting_engine/storage/memory` | 5 | 16 | ✅ PASS | 0.07s |
| `accounting_engine/storage/postgres` | 31 | 61 | ✅ PASS | 107.24s |

**الإجمالي:** 22 حزمة ناجحة، وحزمة واحدة بلا اختبارات (23 حزمة إجمالاً). لا توجد حزم `FAIL`.

## 4. اختبار السباق `-race`

```bash
go test -race -count=1 ./...
```

النتيجة: ✅ **PASS** — لا سباقات بيانات. الزمن الكلي المقاس نحو **226 ثانية**، معظمه في `storage/postgres` (225.9 ثانية) بسبب اختبارات التزامن والضغط. لا يوجد سباق معروف متبقٍ. أي سباق جديد يفشل مهمة `Race detector (PostgreSQL)` في CI.

## 5. التغطية

| القياس | القيمة |
| --- | --- |
| التغطية الإجمالية عند خط الأساس | `54.1%` |
| التغطية الإجمالية بعد أدلة المرحلة 0 | `57.2%` |
| الحد الأدنى المفروض في CI | `54.1%` |
| السكربت | [`scripts/check_coverage.sh`](../scripts/check_coverage.sh) |

```bash
go test -count=1 -coverprofile=coverage.out ./...
COVERAGE_MIN=54.1 ./scripts/check_coverage.sh coverage.out
go tool cover -func=coverage.out | tail -n 25
```

الحد `54.1%` أقل بقليل من القيمة المقاسة، ويُرفع فقط بعد إضافة تغطية فعلية. لا يُخفَّض لتجاوز فشل.

### 5.1 توزيع التغطية حسب الحزمة

| الحزمة | التغطية | الحزمة | التغطية |
| --- | ---: | --- | ---: |
| `accounting_engine` | 58.8% | `internal/posting` | 56.7% |
| `cmd/agy` | 38.4% | `internal/reporting` | 64.8% |
| `internal/apperrors` | 92.1% | `internal/reporting/taxonomy` | 71.8% |
| `internal/consolidation` | 82.6% | `internal/subledgers` | 59.1% |
| `internal/governance` | 66.5% | `internal/subledgers/bank_parsers` | 81.0% |
| `internal/ias12` | 69.5% | `internal/types` | 75.0% |
| `internal/ifrs15` | 65.6% | `internal/valuation` | 71.8% |
| `internal/ifrs16` | 74.9% | `storage/memory` | 32.8% |
| `internal/ifrs9` | 68.7% | `storage/postgres` | 50.2% |
| `internal/ifrsautomation` | 59.0% | `internal/masterdata` | 41.5% |
| `internal/observability` | 86.5% | `internal/policy` | 47.3% |
| **الإجمالي** | **57.2%** | | |

الحزم الأقل تغطية (`storage/memory`, `cmd/agy`, `internal/masterdata`) مرشحة لإضافة اختبارات في المراحل التالية، لكنها لا تخفض الإجمالي تحت خط الأساس.

## 6. اختبارات تكامل PostgreSQL

- **المتغير:** `POSTGRES_URL`
- **القيمة الافتراضية المحلية:** `postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql`
- **قيمة CI:** `postgres://postgres:postgres@localhost:5432/accounting_test?sslmode=disable`
- **الخدمة في CI:** `postgres:18.6` مع `pg_isready`.

### سياسة العزل والتنظيف

كل اختبار تكامل ينشئ **مخططاً معزولاً** باسم `accounting_test_<uuid>` داخل قاعدة الاختبار، ويضبط `search_path` عليه، ثم يشغّل `EnsureSchema`. عند الانتهاء ينفّذ `DROP SCHEMA ... CASCADE` في `t.Cleanup`. لذلك:

- لا تتداخل الاختبارات على نفس الجداول.
- لا تُحذف بيانات خارج المخطط المؤقت.
- قاعدة الاختبار نفسها لا تُعاد إنشاؤها بين الاختبارات؛ الهجرات تُطبَّق مرة واحدة لكل مخطط.

### ملاحظة بيئية مهمة

قاعدة `accounting_test` المحلية قد تحتفظ ببصمة هجرة قديمة. إذا ظهر:

```text
applied migration 202611110006 checksum differs from source
```

فالسبب أن المخطط العام (public) يحمل نسخة سابقة من الهجرة، وليس فشلاً في الكود. الحل الآمن: استخدام قاعدة جديدة، لا حذف بيانات:

```bash
createdb -h /var/run/postgresql -U osm accounting_test_g0_20261112
export POSTGRES_URL='postgres://osm@localhost:5432/accounting_test_g0_20261112?sslmode=disable&host=/var/run/postgresql'
go test -count=1 ./...
```

بهذه الطريقة شُغّل التحقق النهائي للتقرير، ولم تُتخطَّ أي اختبارات PostgreSQL بسبب البصمة.

## 7. كيف تعيد إنتاج هذا التقرير من نسخة نظيفة

```bash
# 1) هوية الالتزام والبيئة
git checkout 6183a84c4b1173eedfcb45cfd9eaf3941ed8dfa2
go version
go env GOOS GOARCH GOVERSION

# 2) قاعدة اختبار جديدة (لا تستخدم قاعدة فيها بيانات)
createdb -h /var/run/postgresql -U osm accounting_test_baseline
export POSTGRES_URL='postgres://osm@localhost:5432/accounting_test_baseline?sslmode=disable&host=/var/run/postgresql'
psql "$POSTGRES_URL" -Atc 'select version();'

# 3) البوابات
gofmt -l .
go vet ./...
go build ./...
go test -count=1 ./...
go test -race -count=1 ./...

# 4) التغطية
go test -count=1 -coverprofile=coverage.out ./...
COVERAGE_MIN=54.1 ./scripts/check_coverage.sh coverage.out

# 5) دليل الفجوات الحمراء (فشل متوقع)
go test -count=1 -tags gapred -run TestGAP ./...

# 6) الهجرات والأمن
make migrations
./scripts/scan_secrets.sh
```

> في CI لا تحتاج الخطوات 2 و5؛ الخدمة تُنشأ تلقائياً واختبارات `gapred` لا تعمل في الخط الافتراضي.

## 8. الفروق عن خط الأساس الأولي

| العنصر | عند `P0-T1` | بعد إنجاز المرحلة 0 |
| --- | --- | --- |
| التغطية | `54.1%` | `57.2%` |
| اختبارات الفجوات | غير موجودة | 14 اختباراً أحمر + 9 `Skip` مبرر |
| ADRs | غير موجودة | ADR-0001..0006 معتمدة |
| CI | غير موجود | 5 مهام على GitHub Actions |
| RLS | اختبارات جزئية | تغطية 27 جدولاً مالياً |
| سياسة الهجرات | غير موثقة | `migration_policy.md` + فحص آلي |
| الأخطاء والمراقبة | متفرقة | `internal/apperrors` + `/metrics` |
| التوثيق | 5 روابط مكسورة | بلا روابط مكسورة + حالات وتواريخ |

هذه الفروق **لا تغيّر سلوك الترحيل المحاسبي**؛ هي أدلة وبنية تحتية. الإصلاحات الوظيفية تبدأ في المرحلة 1.

### 8.1 إصلاحان صغيران ظهرا أثناء التحقق على قاعدة نظيفة

1. **`storage/postgres/events.go`:** كان إدراج/تحديث `subledger_events` يمرر `PayloadJSON == nil` إلى عمود `payload_bytes NOT NULL`، فيفشل بخرق القيد. أصبح `nil` يُطبَّع إلى `[]byte{}`، وهو نفس معنى الحمولة الفارغة. الاختباران `TestPhase3_EventBridgeIdempotencyPayloadConflictPostgres` و`TestPostgresConcurrentIdempotencyHammering` يمران الآن.
2. **`storage/postgres/benchmark_test.go` و`stress_test.go` و`phase6_stress_test.go`:** اختبارات الأداء/الضغط تُتخطى عند تفعيل تغطية الكود لأن عدادات التغطية تخفض القياس وتطيل الزمن دون تغيير السلامة. في الوضع العادي تعمل كاملة، وفي `-race` تعمل مع تخطي تأكيد الإنتاجية فقط. تبقى تأكيدات النزاهة (تسلسل الأرقام، توازن الأرصدة، عدم تسرب goroutines، ثبات الذاكرة) فعالة حيث تعمل الاختبارات.

هذان الإصلاحان لا يغيّران أي قاعدة محاسبية، لكنهما ضروريان ليكون `go test ./...` أخضر على قاعدة PostgreSQL جديدة.

## 9. القيود المعروفة

1. **حماية الفرع غير مفعّلة تقنياً:** المستودع خاص على خطة GitHub مجانية، وGitHub يرفض `branch protection`/`rulesets` (HTTP 403). الخط يعمل على كل PR، لكن منع الدمج يتطلب ترقية الخطة أو مستودعاً عاماً. التفاصيل في [`ci.md`](./ci.md) §7.
2. **`internal/reconciliation` بلا اختبارات مباشرة:** يُغطى سلوكه عبر `internal/reporting` واختبارات التكامل. لا يُعد فشلاً، لكنه مرشح لإضافة اختبارات في `P4-T3`.
3. **زمن `storage/postgres`:** يهيمن على زمن الاختبارات (~107 ثوانٍ عادياً، ~226 ثانية مع `-race`). لا يوجد اختبار معلّق بلا سبب. اختبارات الأداء/الضغط تُتخطى في وضع التغطية لأن العدادات تشوّه القياس.
4. **اختبارات `gapred` تفشل عمداً:** هذا دليل الفجوات، وليس انحداراً. لا تعمل في الخط الافتراضي.
5. **اختبارات الأداء/الضغط ليست بوابة في وضع التغطية:** تُتخطى في `go test -coverprofile`، وتُقاس في الوضع العادي، وتُتخطى فيها تأكيدات الإنتاجية فقط عند `-race`. البوابة الحقيقية للإنتاجية تُقاس في بيئة مخصصة خارج CI الوظيفي.
