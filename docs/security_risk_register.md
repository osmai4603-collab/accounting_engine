# سجل المخاطر الأمنية وخط أساس عزل المستأجرين

- **الحالة:** معتمد
- **التاريخ:** 2026-11-12
- **المالك:** `SEC` + `DBA`
- **المهمة:** `P0-T7`
- **المراجع:** `storage/postgres/rls_test.go`, `storage/postgres/rls_coverage_test.go`, `scripts/scan_secrets.sh`, `.github/workflows/ci.yml`

## 1. نطاق التقييم

| العنصر | ما تم التحقق منه |
| --- | --- |
| عزل الشركات (RLS) | الجداول المالية الرئيسية، سياسات `FORCE ROW LEVEL SECURITY` |
| قراءة/كتابة عبر الشركات | اختبارات RLS القائمة + اختبار تغطية الجداول |
| الأسرار | فحص عالي الثقة محلياً + `gitleaks` في CI |
| الثغرات | `govulncheck` في CI |
| التحليل الساكن | `gosec` (شدة/ثقة عالية) في CI |
| الهجرات | حارس الهجرات + RLS لكل جدول جديد |

## 2. تقرير RLS

### 2.1 آلية العزل

- كل معاملة شركة تستدعي `set_config('app.current_company_id', …, true)` داخل المعاملة.
- الجداول المالية عليها `ROW LEVEL SECURITY` و`FORCE ROW LEVEL SECURITY` وسياسة تقارن `company_id` (أو `id` لجدول `companies`) بالمتغير أعلاه.
- المفاتيح الأجنبية المركبة `(company_id, …)` تمنع ربط كيان بشركة أخرى حتى قبل الترحيل.

### 2.2 الجداول المغطاة

`storage/postgres/rls_coverage_test.go` يتحقق من كل جدول في القائمة التالية: وجود RLS، وFORCE، وسياسة واحدة على الأقل.

| # | الجدول | النوع |
| --- | --- | --- |
| 1 | `companies` | كيانات |
| 2 | `accounts` | دفتر الأستاذ |
| 3 | `fiscal_years` | فترات |
| 4 | `fiscal_periods` | فترات |
| 5 | `journals` | دفتر الأستاذ |
| 6 | `account_moves` | دفتر الأستاذ |
| 7 | `account_move_lines` | دفتر الأستاذ |
| 8 | `subledger_mappings` | دفتر مساعد |
| 9 | `event_accounting_rules` | دفتر مساعد |
| 10 | `subledger_events` | دفتر مساعد |
| 11 | `subledger_open_lines` | دفتر مساعد |
| 12 | `fixed_assets` | أصول |
| 13 | `fixed_asset_depreciations` | أصول |
| 14 | `bank_items` | بنوك |
| 15 | `exchange_rates` | عملات |
| 16 | `financial_audit_logs` | تدقيق |
| 17 | `accounting_configurations` | إعداد |
| 18 | `account_reconciliations` | تسوية |
| 19 | `account_reconciliation_items` | تسوية |
| 20 | `bank_reconciliations` | تسوية |
| 21 | `account_versions` | إصدارات الحسابات |
| 22 | `cost_centers` | أبعاد |
| 23 | `governance_roles` | حوكمة |
| 24 | `user_role_assignments` | حوكمة |
| 25 | `approval_requests` | حوكمة |
| 26 | `approval_steps` | حوكمة |
| 27 | `financial_dimension_values` | أبعاد |

### 2.3 نتيجة التشغيل

| الاختبار | النتيجة | ملاحظة |
| --- | --- | --- |
| `TestPostgresRLSBlocksCrossCompanyEntryReads` | PASS (مع PostgreSQL) | يثبت أن دور غير مالك لا يرى قيد شركة أخرى |
| `TestPostgresRejectsCrossCompanyAccountInPosting` | PASS (مع PostgreSQL) | يثبت رفض القيد الملوث بلا أثر متبقٍ |
| `TestEveryFinancialTableHasForcedRLS` | PASS (مع PostgreSQL) | تغطية 27 جدولاً |
| `scripts/scan_secrets.sh` | PASS | لا أسرار عالية الثقة |
| `govulncheck` / `gosec` / `gitleaks` | CI | تُشغَّل في وظيفة `security` |

> اختبارات PostgreSQL تُكتب Skip مع سبب واضح عند تعذر الاتصال بالخادم. CI يوفّر خدمة PostgreSQL ولا يسمح بالـSkip الصامت في وظيفة التكامل.

## 3. سجل المخاطر

مرتبة حسب الخطورة (Critical/High/Medium/Low)، مع المالك والمهمة.

| ID | المخاطرة | الاحتمال | الأثر | التصنيف | المالك | التخفيف | الحالة |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `SEC-01` | تسرب بيانات بين الشركات عبر استعلام لا يمر بـ`company_id` أو RLS | منخفض | حرج | Critical | `SEC`/`DBA` | RLS + FORCE + اختبار تغطية كل الجداول + مفاتيح مركبة | مُخفَّف جزئياً؛ يستمر الفحص |
| `SEC-02` | تجاوز RLS عبر دور مالك أو `BYPASSRLS` في الإنتاج | منخفض | حرج | Critical | `DBA` | منع الأدوار المالكة للتطبيق، مراجعة صلاحيات النشر، اختبار دور `NOBYPASSRLS` | مفتوح للمراجعة التشغيلية |
| `SEC-03` | سياسة موافقات غير مهيأة تسمح بترحيل غير معتمد | متوسط | عالٍ | High | `LEDGER`/`SRE` | ADR-0002 Fail-Closed + فحص إقلاع + اختبار رفض | مفتوح حتى `P1-T7` |
| `SEC-04` | سر مكشوف في المستودع | منخفض | عالٍ | High | `SEC` | فحص محلي + gitleaks + إدارة أسرار عبر البيئة | مُخفَّف |
| `SEC-05` | ثغرة معروفة في تبعية (Go module) | متوسط | متوسط | High | `SRE` | `govulncheck` في CI + تحديث دوري | مُخفَّف |
| `SEC-06` | هجرة تحذف/تغيّر نوع بيانات مالية | منخفض | عالٍ | High | `DBA` | حارس الهجرات + Expand/Contract + نسخ احتياطي | مُخفَّف |
| `SEC-07` | خدمة `/metrics` مكشوفة بلا auth/TLS/حدود | متوسط | متوسط | Medium | `SRE` | `P4-T1`: readiness/auth/TLS/limits | مفتوح حتى `P4-T1` |
| `SEC-08` | سجلات تحتوي بيانات حساسة | منخفض | متوسط | Medium | `SRE` | حقول ارتباط فقط، لا مبالغ/أسماء عملاء | مُخفَّف |
| `SEC-09` | إعادة تشغيل أتمتة تفقد حالة/تكرر الترحيل | متوسط | عالٍ | High | `SRE`/`LEDGER` | `P4-T2` Outbox/DLQ/Idempotency | مفتوح |
| `SEC-10` | غياب اختبار استعادة/PITR | متوسط | عالٍ | High | `SRE` | `P4-T5` تقرير DR | مفتوح |

## 4. ضوابط إلزامية قبل الإنتاج

1. تشغيل دور التطبيق بـ`NOBYPASSRLS` وليس مالك الجداول.
2. عدم استخدام حساب `postgres` فائق الصلاحية في التطبيق.
3. `StrictGovernance=true` وسياسة شركة مهيأة.
4. تشغيل `govulncheck` و`gosec` و`gitleaks` بلا استثناءات غير موثقة.
5. مراجعة أي هجرة جديدة عبر حارس الهجرات + مراجعة `DBA`.
6. تشغيل `TestEveryFinancialTableHasForcedRLS` في CI لكل PR يمس الهجرات أو التخزين.

## 5. إعادة إنتاج الفحوص

```bash
./scripts/scan_secrets.sh
POSTGRES_URL='postgres://user@localhost:5432/accounting_test?sslmode=disable' \
  go test -count=1 ./storage/postgres -run 'TestPostgresRLS|TestEveryFinancialTableHasForcedRLS'
go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...
go install github.com/securego/gosec/v2/cmd/gosec@latest && gosec -quiet -severity high -confidence high ./...
```
