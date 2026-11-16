# خط التكامل المستمر (CI) وبوابات الدمج

- **الحالة:** نشطة
- **التاريخ:** 2026-11-12
- **المالك:** `SRE` + `QA`
- **المهمة:** `P0-T2`
- **الملف:** [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)
- **التقرير المرتبط:** [`engine5_baseline.md`](./baselines/engine5_baseline.md)

## 1. الهدف

منع دمج أي تغيير يكسر التنسيق أو البناء أو الاختبارات أو التهجير أو التغطية أو الفحص الأمني. الخط يعمل تلقائياً على:

- `push` إلى `main` و`release/**`
- `push` لوسوم الإصدارات `v*`
- `pull_request` إلى `main` و`release/**`
- `workflow_dispatch` يدوياً

## 2. المهام (Jobs)

| المهمة | الاسم في GitHub | ما تفعله | شرط الفشل |
| --- | --- | --- | --- |
| `quality` | Static, architecture, API + external consumer | protobuf drift, gofmt, vet/build, public API/internal-import purity, Posting Engine composition-root boundaries, API compatibility, published-module consumer, module integrity, وصورة Docker | فشل أي خطوة |
| `test` | Unit, contract + golden accounting (PostgreSQL) | `go test -json -count=1 -coverprofile=coverage.out ./...` مع خدمة PostgreSQL؛ external consumers وGAPRED tagged tests وتقرير Contract Parity | فشل أي اختبار أو انخفاض التغطية عن `54.1%` |
| `service-integration` | Integration — service runtime + protocols (PostgreSQL) | تشغيل الخدمة والتحقق من HTTP/gRPC مقابل PostgreSQL | فشل البناء أو التشغيل أو أي probe |
| `race` | Concurrency — race detector (PostgreSQL) | `go test -race -count=1 ./...` مع خدمة PostgreSQL | أي سباق بيانات |
| `migrations` | Migration policy + upgrade (PostgreSQL) | `make migrations` — فحص الهجرات القابلة للعكس/الخطرة + دورة ترقية كاملة | هجرة غير قابلة للعكس، DDL خطير بلا موافقة، أو فشل الترقية |
| `security` | Security, dependency + SBOM gates | فحص الأسرار، `go-licenses` وفق قائمة SPDX المعتمدة، `govulncheck`, `gosec` عالي الخطورة، `gitleaks`، سلامة وحدات Go، وSBOM بصيغة CycloneDX | فشل أي فحص أو تعذر إنشاء SBOM |
| `release-gate` | Release gates | على وسوم `v*` فقط، يفشل ما لم تنجح كل البوابات الحرجة | أي job فاشل أو ملغى أو متخطى |

كل المهام تعمل على `ubuntu-latest`. إصدار Go يُقرأ من `go.mod` عبر `go-version-file`، فلا يوجد إصدار مكرر في أكثر من مكان.

> **سياسة تراخيص التبعيات:** تُقبل فقط `MIT`, `MIT-0`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `0BSD`, `Zlib`, `BSL-1.0`, `CC0-1.0`, و`Unlicense`. تفحص البوابة اعتماديات الوحدات الثلاث، بما فيها اعتماديات الاختبارات، وترفع تقرير CSV مستقلًا لكل وحدة؛ أي ترخيص مجهول أو غير مدرج يفشل البوابة.

## 3. خدمة PostgreSQL

المهام `test` و`service-integration` و`race` و`migrations` تشغّل خدمة:

```yaml
image: postgres:18.6
POSTGRES_USER: postgres
POSTGRES_PASSWORD: postgres
POSTGRES_DB: accounting_test
ports: ["5432:5432"]
```

مع `pg_isready` كفحص صحة. متغير الاتصال:

```text
postgres://postgres:postgres@localhost:5432/accounting_test?sslmode=disable
```

يستخدم Compose أيضاً `postgres:18.6` حتى تتطابق البيئة المحلية مع CI وخط الأساس. يتطلب Compose ضبط `POSTGRES_PASSWORD` و`DATABASE_DSN` في البيئة؛ لا تُحفظ بيانات الاعتماد في ملفات المصدر أو سجلات البناء.

## 4. التغطية

- الحد الأدنى الحالي: **`54.1%`** (خط الأساس المقاس `54.1%`).

> الخط الأدنى لا يقل عن خط الأساس. القيمة المقاسة بعد إنجاز المرحلة 0 أعلى قليلاً، لكن الحد يُثبَّت عند خط الأساس لمنع أي انخفاض.

- السكربت: [`scripts/check_coverage.sh`](../scripts/check_coverage.sh)
- يقرأ `go tool cover -func` ويفشل إذا نزل الإجمالي عن الحد.
- تُرفع تقارير التغطية والاختبارات وContract Parity كأثر (artifact) لمدة 14 يوماً للمراجعة.

رفع الحد يتم فقط بعد إضافة تغطية فعلية، لا بتخفيضه لتجاوز فشل.

> **ملاحظة:** في مهمة `test` (وضع التغطية) تُتخطى اختبارات الأداء/الضغط لأن عدادات التغطية تشوّه الإنتاجية وتطيل الزمن. تبقى هذه الاختبارات تعمل كاملة في مهمة `race` (مع تخطي تأكيد الإنتاجية فقط) وفي التشغيل المحلي العادي، حيث تُثبت النزاهة والتزامن.

## 5. اختبارات الفجوات الحمراء

اختبارات `GAP-*` تحمل وسم بناء `gapred` ولذلك **لا تعمل** في الخط الافتراضي. هذا مقصود: الخط يبقى أخضر على الكود الحالي، بينما يبقى الدليل الأحمر قابلاً للتشغيل:

```bash
go test -count=1 -tags gapred -run TestGAP ./...
```

عند بدء إصلاح فجوة في مرحلة لاحقة، يُزال الوسم أو يُحوَّل الاختبار إلى اختبار انحدار دائم. التفاصيل في [`ENGINE_COMPLIANCE_MATRIX.md`](./ENGINE_COMPLIANCE_MATRIX.md).

## 6. التشغيل المحلي

الواجهة الموحدة هي `Makefile`:

```bash
make ci          # كل البوابات (يتطلب PostgreSQL محلياً)
make fmt-check   # gofmt فقط
make vet         # go vet
make build       # go build
make test        # go test ./...
make test-contract-parity # اختبارات التكافؤ وتوليد التقرير
make race        # go test -race ./...
make coverage    # coverage.out + فرض الحد الأدنى
make migrations  # فحص الهجرات + اختبار الترقية
make architecture-check # API purity + حدود بناء Posting Engine
make security    # govulncheck وgosec وفحص الأسرار (الأدوات مطلوبة)
```

المتغيرات القابلة للتجاوز:

```bash
COVERAGE_MIN=55.0 make coverage
POSTGRES_URL='postgres://postgres:postgres@localhost:5432/accounting_test?sslmode=disable' make test
```

على جهاز التطوير المحلي حيث يستخدم PostgreSQL مقبس Unix:

```bash
export POSTGRES_URL='postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql'
```

> إذا كانت `accounting_test` تحتفظ بهجرات قديمة، فقد تظهر `applied migration ... checksum differs from source`. استخدم قاعدة جديدة بدلاً من حذف بيانات، كما في [`engine5_baseline.md`](./baselines/engine5_baseline.md) §6.

## 7. إلزامية الخط وحماية الفرع

ملف CI وحده **لا يمنع الدمج**. يجب تفعيل الحماية في إعدادات المستودع على GitHub:

1. `Settings → Branches → Add branch protection rule` (أو `Settings → Rules → Rulesets`).
2. الهدف: `main` و`release/**`.
3. تفعيل `Require a pull request before merging`.
4. تفعيل `Require status checks to pass before merging`.
5. اختيار الفحوص التالية كإلزامية:
   - `Static, architecture, API + external consumer`
   - `Unit, contract + golden accounting (PostgreSQL)`
   - `Integration — service runtime + protocols (PostgreSQL)`
   - `Concurrency — race detector (PostgreSQL)`
   - `Migration policy + upgrade (PostgreSQL)`
   - `Security, dependency + SBOM gates`
   - `Release gates` عند التحقق من الوسم
6. تفعيل `Require branches to be up to date before merging`.
7. منع `force push` وحذف الفرع المحمي.

> **دليل الإثبات المطلوب:** لقطة من إعداد الحماية تُظهر الفحوص الخمسة كإلزامية، ولقطة من PR فاشل محجوب، ولقطة من PR ناجح. لا يمكن حفظ إعدادات GitHub داخل المستودع، لذلك تُحفظ اللقطات في سجل المرحلة أو مرفقات المراجعة.

### 7.1 حالة التفعيل الفعلية (2026-11-12)

تم التحقق عبر GitHub API:

```text
GET /repos/osmai4603-collab/accounting_engine/branches/main/protection  -> 403
GET /repos/osmai4603-collab/accounting_engine/rulesets                  -> 403
```

السبب: المستودع **خاص** على خطة GitHub مجانية، والميزة تتطلب GitHub Pro أو مستودعاً عاماً. لذلك:

- الخط يعمل تلقائياً على كل PR، لكنه **غير إلزامي تقنياً** بعد.
- لا يمكن اعتبار بند «CI يحجب الدمج» مغلقاً حتى تُرقّى الخطة أو يتغير نطاق المستودع.
- بعد الترقية، تُطبَّق الخطوات أعلاه دون أي تغيير في ملف CI.

## 8. سلوك متوقع عند الفشل

| السيناريو | النتيجة |
| --- | --- |
| ملف Go غير منسق | `quality` يفشل ويطبع الملفات؛ الحل `gofmt -w` |
| اختبار فاشل | `test` وربما `race` يفشلان؛ لا دمج |
| تغطية أقل من `54.1%` | `test` يفشل عند خطوة `coverage floor` |
| هجرة بلا `down` أو DDL خطير | `migrations` يفشل |
| سر/ثغرة عالية | `security` يفشل |
| تعارض في `go.sum` أو بناء | `quality` يفشل عند `go build` |

## 9. الصيانة

- تحديث `postgres:18.6` يتم في مكان واحد مع تحديث خط الأساس.
- أي فحص جديد يُضاف إلى `Makefile` وأيضاً إلى `ci.yml` في نفس الالتزام.
- تُراجع مدة المهام شهرياً؛ الحدود الحالية: 15/30/40/20/20 دقيقة.
- لا يُضاف `continue-on-error` لأي بوابة إلزامية.

تعمل الاختبارات مرة واحدة دون إعادة محاولة تلقائية أو تجاوز للفشل. لا توجد
آلية استثناء أو whitelist لتجاوز بوابة؛ تُصلح الاختبارات المتذبذبة أو تُحذف
بتغيير عادي يخضع للمراجعة، ولا يجوز إخفاء فشلها.

يرفع CI تقارير static/API compatibility، external consumer، الاختبارات
والتغطية وContract Parity، integration، concurrency، migrations، وفحوص الأمان
وSBOM عند توفرها. الاحتفاظ 14 يوماً، و30 يوماً لتقارير الأمن.

### 9.1 التحقق من وسم الإصدار

يُشغّل كل `v*` عبر البوابات نفسها، ثم يعمل `Release gates` كحاجز تجميعي
ويفشل إذا لم تنجح جميعها. لا ينشئ CI إصدار GitHub تلقائياً؛ يجب أن تنتظر
أتمتة النشر نجاح `CI / Release gates`. GitHub يسمح بإنشاء الوسم قبل اكتمال
الفحوص، لذلك فشل البوابة يمنع اعتباره إصداراً موثقاً لكنه لا يحذف الوسم.

لا توجد حالياً سياسة معتمدة لتراخيص الاعتماديات أو جهة/إجراء للموافقة على
استثناءاتها. يُنشر SBOM للمراجعة، ولا يخترع CI قائمة تراخيص مسموح بها قبل
اعتماد سياسة حوكمة واضحة.
