# ARD-P6-03: إثبات Tenant/Company Isolation وPostgreSQL RLS اعتمادًا على Execution Context موثوق

- **المهمة:** `P6-03`
- **المرحلة:** `P6`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة
- **الملفات المعنية:** `storage/postgres/` (`internal/adapters/postgres/`), `transactions.go`, `phase6_rls_penetration_test.go`, `rls_coverage_test.go`, `rls_test.go`

## 1. السياق والمشكلة (Context & Problem)

تطبيق سياسات الأمان على مستوى الصفوف (Row-Level Security - RLS) في قاعدة البيانات وحدها لا يكفي لإثبات عزل البيانات بين الشركات والمستأجرين، ما لم يتم التحقق من أن:
1. `app.current_company_id` يُضبط على مستوى المعاملة (`set_config(..., true)`) لا على مستوى الجلسة ككل، ويتم تنظيفه تلقائيًا عند انتهائها.
2. غياب `CompanyID` في `ExecutionContext` أو استخدام معرف شركة غير مطابق يتم رفضه صراحة قبل تنفيذ الاستعلامات والعمليات.
3. التخزين المؤقت (Connection Pool) لا يسرب سياق شركة إلى شركة أخرى عند إعادة استخدام الاتصال.
4. جميع جداول البيانات المؤسسية والمالية (42 جدولًا) مغطاة بـ RLS ومفعلة بـ `FORCE ROW LEVEL SECURITY` وسياسات عزل متوافقة.
5. استعلامات الوصول بين الشركات (Cross-Company Reads) ومحاولات ربط حساب من شركة بأخرى ترفض وتفشل عند مستوى RLS التابع لـ PostgreSQL.
6. المحرك لا يتضمن أي منطق يتعلق بإدارة الهوية أو صلاحيات المستخدمين أو عضوية الشركة (Membership/Authorization)، والتي تبقى مسؤولة بالكامل من النظام المضيف.

## 2. القرارات والتنفيذ (Decisions & Implementation)

1. **الضبط المحلي لسياق الشركة (`Transaction-Local Scope`):**
   - في `transactions.go` (`beginCompanyTx` و`ExecuteInTransaction`) يتم تنفيذ:
     `SELECT set_config('app.current_company_id', $1, true)`
   - استخدام المعلمة `true` تجعل الضبط محليًا داخل حدود المعاملة فقط (`SET LOCAL`) لتضمن عدم تسرب المعرف عبر الاتصال في المجمع (`Connection Pool`).

2. **فحص النقاء وحماية المعاملة من التعارض:**
   - يرفض `beginCompanyTxUnscoped` و`beginCompanyTx` أي طلب بشركة `uuid.Nil` أو معرف غير مطابق لمعاملة جارية فورًا بخطأ واضح دون إجراء استعلام DB.

3. **اختبارات الاختراق والعزل الشاملة (`RLS Penetration Suite`):**
   - `TestEveryFinancialTableHasForcedRLS`: يتحقق حركيًا من تغطية 42 جدولًا مؤسسيًا/ماليًا في PostgreSQL بـ `RLS` و`FORCE ROW LEVEL SECURITY` وسياسة عزل شركات.
   - `TestPhase6RLSPenetrationCrossCompanyReads`: يثبت عجز دور التطبيق المقيد (`accounting_app_user`) عن قراءة قيود شركة أخرى حتى عند ضبط `app.current_company_id` بمحاولة اختراق.
   - `TestPhase6RLSPenetrationCrossCompanyAccountInEntry`: يثبت رفض إنشاء قيد يتضمن حسابات تنتمي لشركتين مختلفين بواسطة RLS وForeign Keys.
   - `TestPhase6RLSPenetrationMissingCompanyContext`: يثبت رفض عمليات القراءة والكتابة عند غياب `app.current_company_id`.

4. **استقلال المصادقة والعزل:**
   - المحرك يتأكد من مطابقة `CompanyID` ويعتمد RLS لدفاع DB، بينما يترك مصادقة وتفويض المستخدم للنظام المضيف.

## 3. إثبات معايير القبول (Verification Evidence)

| معيار القبول | الدليل الاختباري | النتيجة |
| --- | --- | --- |
| منع القراءة/التعديل بين الشركات عند معرفة UUID | `TestPhase6RLSPenetrationCrossCompanyReads` | محقق |
| رفض غياب أو عدم اتساق Company Context | `TestPhase6RLSPenetrationMissingCompanyContext` & `beginCompanyTx` | محقق |
| ضبط Company Context داخل المعاملة فقط وزواله بانتهائها | `transactions.go` (`set_config(..., true)`) | محقق |
| اختبار إعادة استخدام المجمع دون تسريب بيانات | `TestPostgresRLSBlocksCrossCompanyEntryReads` | محقق |
| جداول البيانات المؤسسية محمية بـ RLS إلزامية | `TestEveryFinancialTableHasForcedRLS` (42 جدولًا) | محقق على PostgreSQL |
| استقلال المحرك عن إدارة المستخدمين والأدوار | `docs/adr/ADR-0004-auth-and-company-context.md` | محقق |

## 4. مراجعة تغطية جداول RLS (2026-11-15)

أظهرت مراجعة سلسلة الترحيلات أن اختبار التغطية كان يغفل عشرة جداول مؤسسية أضيفت في ترحيلات لاحقة. أُضيفت هذه الجداول إلى `TestEveryFinancialTableHasForcedRLS`، وأُضيف ترحيل يفرض RLS على جدولي `public_event_ingestions` و`public_event_account_mappings` اللذين كانا مفعّلين بسياسات عزل دون `FORCE ROW LEVEL SECURITY`. أصبح الاختبار يتحقق من 42 جدولًا.

نُفذت اختبارات العزل والتغطية على PostgreSQL بنجاح:

```text
go test ./internal/adapters/postgres -run 'Test(PostgresUnitOfWorkDoesNotLeakCompanyOrActorContextThroughPool|PostgresUnitOfWorkRollsBackAndScopesTransaction|PostgresRLSBlocksCrossCompanyEntryReads|PostgresRejectsCrossCompanyAccountInPosting|Phase6RLSPenetration.*|EveryFinancialTableHasForcedRLS)$' -count=1
ok   github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres
```

## 5. الحدود (Limits)

- تقتصر هذه المهمة على إثبات RLS والعزل المؤسسي داخل PostgreSQL و`storage/postgres`.
- إدارة هوية المستخدم وصلاحياته تتبع النظام المضيف وتتصل بالخدمة المستقلة عبر `P6-02`.
