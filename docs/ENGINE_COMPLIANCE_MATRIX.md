# ENGINE_COMPLIANCE_MATRIX.md

## مصفوفة مطابقة Accounting Engine للمعايير الهندسية والمحاسبية

**الإصدار:** 1.0  
**تاريخ المراجعة:** 2026-11-13  
**نسخة المصدر التي تمت مراجعتها:** `accounting_engine.zip` (ملفات المستودع بتاريخ 2026-11-10)  
**الغرض:** تحويل وثيقة المعايير الهندسية والمحاسبية إلى قائمة تحقق قابلة للتتبع مرتبطة بالكود والاختبارات.

---

## 1. كيف تقرأ هذه المصفوفة؟

هذه المصفوفة تقيس ما يظهر في نسخة المستودع التي تمت مراجعتها، ولا تمنح شهادة امتثال أو اعتماداً محاسبياً.

### حالات المطابقة

| الحالة | معناها |
| --- | --- |
| **مطبق** | توجد آلية واضحة في الكود، ومعها اختبار مناسب أو دليل تنفيذ مباشر. |
| **جزئي** | توجد بنية أو بعض الوظائف، لكن توجد فجوات أو لا تغطي جميع المسارات. |
| **غير مطبق** | لم يظهر تنفيذ كافٍ للمتطلب في النسخة التي تمت مراجعتها. |
| **غير مثبت** | توجد وثائق أو اختبارات تشير إلى التنفيذ، لكن التحقق العملي المطلوب لم يكتمل. |
| **يحتاج قراراً** | المطلوب تحديد سياسة أو قاعدة محاسبية قبل الحكم على التنفيذ. |

### الأولوية

- **P0 – مانع إنتاج:** قد يهدد صحة القيد أو الأرصدة أو سلامة المعاملة أو العزل.
- **P1 – مرتفع:** يؤثر في الرقابة أو الاعتمادية أو صحة وحدة رئيسية.
- **P2 – متوسط:** يؤثر في قابلية الصيانة أو التوسع أو اكتمال الوظائف.
- **P3 – تحسين:** تحسين مرغوب لا يمنع الاستخدام وحده.

> لا تعتبر كلمة «مطبق» إثباتاً بأن التنفيذ خالٍ من العيوب؛ إنها تعني فقط أن هناك تنفيذاً واختبارات ذات صلة. كما أن وجود ملف اختبار لا يعني أن الاختبار نُفّذ بنجاح على PostgreSQL.

---

# 2. الملخص التنفيذي

| المجال | الحالة العامة | الأولوية الغالبة |
| --- | --- | --- |
| التصميم المعماري | جزئي إلى جيد | P2 |
| صحة القيود والأموال | جزئي | P0 |
| Posting Engine | جزئي | P0 |
| تعدد العملات | جزئي | P0 |
| الفترات والأرصدة | جزئي إلى جيد | P1 |
| Multi-Tenancy | جزئي إلى جيد | P0/P1 |
| Subledgers | جزئي | P1 |
| الحوكمة والموافقات | جزئي | P0/P1 |
| Audit Trail | جزئي | P0/P1 |
| التقارير | جزئي إلى جيد | P1 |
| IFRS / IAS | تنفيذ جزئي حسب المعيار | P1/P2 |
| الاختبارات | جيدة كبنية، لكن التحقق الإنتاجي غير مكتمل | P0/P1 |
| التشغيل والإنتاج | غير مثبت بالكامل | P1 |

**الاستنتاج:** المستودع يحتوي على تنفيذ واسع ومكونات حقيقية، لكنه لا يجتاز بعد بوابة الجاهزية الإنتاجية. يجب إغلاق بنود P0 أولاً، ثم استكمال اختبارات التكامل والتشغيل قبل وصفه بأنه Production Ready.

---

# 3. المصفوفة التفصيلية

## A. التصميم المعماري

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| ARCH-01 | فصل Domain عن التخزين والبنية التحتية | جزئي | P2 | `internal/`، `storage/memory/`، `storage/postgres/` | تدقيق الاعتماديات لمنع تسرب تفاصيل PostgreSQL إلى Domain. |
| ARCH-02 | فصل المسؤوليات حسب المجال | مطبق جزئياً جيداً | P2 | `internal/masterdata/`, `posting/`, `reporting/`, `subledgers/`, `governance/`, `valuation/` | توثيق حدود كل Module ومنع التداخل التدريجي. |
| ARCH-03 | واجهات صغيرة حسب الحاجة | جزئي | P2 | `engine.go` — `StorageDriver` | تفكيك الواجهة الجامعة إلى Ports أصغر لكل Use Case. |
| ARCH-04 | استقلال المحرك عن النظام المستهلك | جزئي | P1 | `internal/subledgers/event_bridge.go`, `event_formula.go` | تعريف عقود Events مستقرة واختبار ربط المحرك بتطبيق خارجي دون تعديل النواة. |
| ARCH-05 | Type Safety في إنشاء المحرك | يحتاج تحسيناً | P2 | `engine.go` — `New(first, second any)` | استبدال `any` بتوقيع صريح أو Constructors منفصلة. |
| ARCH-06 | تمرير Context عبر العمليات | جزئي | P1 | `internal/posting/engine.go` مقابل `internal/subledgers/event_bridge.go` | إزالة `context.Background()` من المسارات التي يجب أن تستقبل Context من المستدعي. |
| ARCH-07 | عدم الاعتماد على ذاكرة نسخة واحدة | جزئي | P1 | `EventBridge`, `ApprovalEngine`, `Scheduler` | تحديد ما هو Cache وما هو State دائم، واختبار تشغيل أكثر من نسخة. |
| ARCH-08 | توثيق قرارات المعمارية | مطبق جزئياً | P2 | `docs/engineering_principles.md`, `docs/key_architectural_decisions_analysis.md` | ربط كل قرار معماري بقرار ADR وحالة التنفيذ والاختبارات. |

## B. الصحة المحاسبية ونموذج الأموال

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| ACC-01 | استخدام Decimal بدلاً من Float للحسابات المالية | مطبق | P1 | `internal/types/money.go`, `money_test.go` | الاستمرار في منع Float في جميع المسارات المالية. |
| ACC-02 | توازن القيد على أساس العملة الوظيفية | يحتاج إصلاحاً | P0 | `storage/postgres/schema.sql` — `check_posted_move_balance_statement()` | التحقق من `functional_debit_minor` مقابل `functional_credit_minor` بدلاً من جمع مبالغ المعاملات بعملات قد تختلف. |
| ACC-03 | توحيد الدقة والتقريب بين Domain وDB والتقارير | جزئي | P0 | `internal/types/money.go` — `FinancialScale`, `MinorUnits()`, `RoundForPosting()`؛ `schema.sql` | اعتماد سياسة موحدة موثقة لكل عملة، وتحديد موضع التقريب، واختبار تطابق الأرصدة. |
| ACC-04 | تمييز Transaction / Functional / Presentation Currency | جزئي إلى جيد | P1 | `internal/types/currency.go`, `internal/valuation/`, `internal/masterdata/company.go`, `internal/reporting/currency_translation.go` | اختبار دورة العمل كاملة من القيد إلى التقييم والتقرير، مع حالات العملات المختلفة. |
| ACC-05 | منع تعديل القيود المرحّلة | مطبق مع ضرورة استكمال الاختبارات | P1 | `storage/postgres/schema.sql` — `protect_posted_move_lines()`؛ `storage/postgres/immutability_test.go` | إضافة تغطية لكل محاولات تعديل الرأس والأسطر والبيانات المرتبطة. |
| ACC-06 | عكس القيد دون حذف الأصل | مطبق جزئياً | P1 | `internal/posting/reversal.go`; `storage/postgres/driver.go` — `PostReversalAtomicContext()` | إثبات منع العكس المكرر بالتزامن وعلى PostgreSQL، واختبار الفترة وتاريخ العكس. |
| ACC-07 | سلامة الحسابات العكسية Contra Accounts | موجود | P2 | `internal/masterdata/account.go` | إضافة اختبارات تقارير تثبت عرض الرصيد الطبيعي للحسابات العكسية. |
| ACC-08 | منع الحسابات غير القابلة للترحيل | مطبق جزئياً | P1 | `internal/posting/engine.go` — `prepareForPosting()`؛ `internal/masterdata/` | إضافة اختبارات PostgreSQL للحسابات غير النشطة/التجميعية/خارج الشركة. |
| ACC-09 | صحة الفترات العادية وفترات التسوية | مطبق جزئياً | P1 | `internal/masterdata/fiscal_calendar.go`; `internal/posting/engine.go` | اختبار الحدود الزمنية، التداخل، إعادة الفتح، والفترتين 13 و14. |
| ACC-10 | إعادة احتساب الأرصدة من دفتر الأستاذ | جزئي | P0 | `storage/postgres/migrations/202611100009_perpetual_account_balances.sql`; `internal/reporting/` | إضافة Rebuild/Reconciliation موثق ومقارنة يومية بين الرصيد المخزن والرصيد المعاد احتسابه. |

## C. Posting Engine والمعاملات

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| POST-01 | وجود مسار ترحيل ذري مع تخصيص التسلسل | مطبق في المسار الأساسي | P0 | `internal/posting/engine.go` — `PostEntryContext()`؛ `storage/postgres/driver.go` — `postAndSequenceContext()` | جعل هذا المسار هو الطريق الوحيد لترحيل الإنتاج. |
| POST-02 | منع تجاوز Posting Engine | جزئي | P0 | `storage/postgres/driver.go` — `PostBulk()` و`saveEntryTx()` | منع أو تقييد استدعاء مسارات التخزين التي تحول الحالة إلى Posted دون كل تحقق وتسلسل وصلاحيات. |
| POST-03 | صحة Bulk Posting | يحتاج إصلاحاً | P0 | `internal/posting/bulk.go`; `storage/postgres/driver.go` — `PostBulk()` | توحيد معنى Bulk، وتخصيص التسلسل، والتحقق، والأرصدة، والحوكمة، وتحديد Atomic Batch أو Partial Success صراحة. |
| POST-04 | سلامة تخصيص أرقام اليومية تحت التزامن | جزئي | P0 | `storage/postgres/driver.go` — `GetNextSequenceWithLock()` و`postAndSequenceContext()` | الدالة المسماة `GetNextSequenceWithLock` تقرأ `current_sequence + 1` دون `FOR UPDATE` أو تحديث؛ يجب منع استخدامها كحجز تسلسل مستقل. |
| POST-05 | حدود Transaction واضحة | مطبق جزئياً | P0 | `storage/postgres/driver.go` — `postAndSequenceContext()`, `PostReversalAtomicContext()` | توحيد المعاملات التي تحدث القيد والأرصدة والدفتر المساعد والتدقيق. |
| POST-06 | Rollback عند الفشل | غير مثبت بالكامل | P0 | `storage/postgres/driver.go`; `storage/postgres/phase2_subledger_test.go` | تشغيل اختبارات الفشل على قاعدة نظيفة وإضافة Fault Injection لانقطاع الاتصال بعد بدء المعاملة. |
| POST-07 | Idempotency للترحيل والأحداث | جزئي | P1 | `internal/subledgers/event_bridge.go`; `storage/postgres/driver.go` — `SaveEventAndDraft()` | مقارنة بصمة Payload عند إعادة استخدام المفتاح، واختبار التزامن وإعادة التشغيل والتخزين الدائم. |
| POST-08 | منع الترحيل المكرر | مطبق جزئياً | P1 | `internal/posting/engine.go`; `storage/postgres/driver.go` | اختبار طلبين متزامنين لنفس القيد على PostgreSQL والتأكد من أثر واحد. |
| POST-09 | تحقق الصلاحيات قبل الترحيل | جزئي | P1 | `internal/posting/engine.go`; `internal/governance/` | توصيل Authorization Policy بمسار Posting بدلاً من الاكتفاء بوجود محرك Governance منفصل. |
| POST-10 | حفظ أثر التدقيق ضمن العملية المالية | جزئي / غير مكتمل التكامل | P0 | `internal/governance/audit_chain.go`; `storage/postgres/driver.go` — `AppendAuditLogAtomic()` | ربط أحداث Posting/Reversal/Period Close بتسجيل تدقيق ذري وفق سياسة إلزامية. |

## D. Multi-Tenancy والعزل

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| TEN-01 | وجود Company Scope في الكيانات | مطبق جزئياً جيداً | P1 | `internal/masterdata/`, `internal/posting/`, `storage/postgres/schema.sql` | مراجعة كل جدول وكيان تابع لضمان عدم وجود بيانات مالية بلا نطاق شركة. |
| TEN-02 | تطبيق RLS | مطبق مع حاجة لاختبارات أوسع | P1 | `storage/postgres/migrations/202611100007_force_tenant_rls.sql`; `storage/postgres/rls_test.go` | اختبار جميع الجداول الحساسة ودور التطبيق الفعلي، وليس مالك قاعدة البيانات فقط. |
| TEN-03 | ضبط سياق الشركة داخل المعاملة | مطبق | P1 | `storage/postgres/driver.go` — `beginCompanyTx()` | إضافة اختبار Pool يثبت عدم تسرب سياق الشركة بين الاتصالات. |
| TEN-04 | منع العمليات الحساسة دون Company ID | جزئي | P0 | `storage/postgres/driver.go`; `engine.go` | إزالة أو تعطيل واجهات Facade غير المحددة بالشركة في مسارات PostgreSQL. |
| TEN-05 | منع الوصول المتقاطع بين الشركات في كل المسارات | غير مثبت بالكامل | P0 | `storage/postgres/rls_test.go`; `internal/posting/engine_test.go` | إضافة اختبار ترحيل قيد شركة A باستخدام حساب شركة B مباشرة على PostgreSQL. |

## E. الدفاتر المساعدة

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| SUB-01 | وجود وحدات AR/AP/Assets/Treasury | مطبق كهيكل ووظائف أساسية | P2 | `internal/subledgers/receivable.go`, `payable.go`, `fixed_assets.go`, `treasury.go` | قياس اكتمال دورة الحياة لكل دفتر، وليس مجرد وجود ملفات. |
| SUB-02 | مطابقة الدفتر المساعد مع GL | جزئي | P0 | `internal/subledgers/gl_reconciliation.go`, `control_accounts.go`, `gl_reconciliation_test.go` | تعريف قواعد المطابقة لكل نوع دفتر وحالات الفروقات والتسوية. |
| SUB-03 | ذرية تحديث الدفتر المساعد مع القيد | جزئي | P0 | `storage/postgres/driver.go` — `SaveOpenLine...`, `ConfirmFixedAssetMutation()`, `CompareAndSave...AndDraft()` | إثبات أن كل عملية لا تترك GL والدفتر المساعد في حالتين متعارضتين عند الفشل أو التزامن. |
| SUB-04 | منع تكرار عمليات الأصل الثابت | جزئي | P1 | `internal/subledgers/fixed_assets.go`; `storage/postgres/driver.go` — `ConfirmFixedAssetMutation()` | إضافة Pending Operation/Unique Reference ومنع تكرار Disposal أو Impairment بالتزامن. |
| SUB-05 | إنشاء خدمات Subledger بنطاق شركة | جزئي | P1 | `internal/subledgers/service.go`; `engine.go` — `NewEngine()` | مراجعة إنشاء الخدمة العامة مقابل `NewServiceForCompany()`، وإتاحة سياق الشركة في واجهة Facade. |
| SUB-06 | إعادة المحاولة الآمنة | جزئي | P1 | `internal/subledgers/event_bridge.go`; `writeoff.go`; `fixed_assets.go` | تحديد أخطاء قابلة لإعادة المحاولة، واستخدام مفاتيح تكرار ثابتة، واختبار فشل ما بعد الترحيل. |
| SUB-07 | استيراد كشوف البنوك | مطبق جزئياً | P2 | `internal/subledgers/bank_parsers/` | إضافة اختبارات ملفات واقعية، وتحقق التكرار، والتعامل مع تنسيقات غير صالحة. |

## F. الحوكمة والموافقات والتدقيق

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| GOV-01 | وجود RBAC | مطبق جزئياً | P1 | `internal/governance/rbac.go`, `repository.go` | إثبات فرض الصلاحيات عند كل Use Case حساس. |
| GOV-02 | Approval Workflow متعدد الخطوات | مطبق جزئياً | P1 | `internal/governance/approval.go`; `phase3_test.go` | ربط الموافقات ببيانات القيد المحفوظة وبسياسة الشركة. |
| GOV-03 | التحقق من مبلغ الموافقة مقابل القيد | غير مكتمل | P0 | `ApprovalEngine.Start()` يقبل المبلغ من المستدعي؛ `IsApproved()` يتحقق من حالة الطلب حسب Entry ID | جلب القيد وحساب قيمته الوظيفية داخل الخدمة، وربط الموافقة ببصمة محتواه. |
| GOV-04 | منع طلبات الموافقة المتعددة المتعارضة للقيد | غير مكتمل | P1 | `storage/postgres/migrations/202611100005_phase3_governance.sql` — `approval_requests` | إضافة قيد فريد مناسب أو نموذج يسمح بإصدارات/إبطال صريحة. |
| GOV-05 | تفعيل Approval Gate في التهيئة الافتراضية | غير مفعل تلقائياً | P0 | `engine.go` — `NewEngine()` لا يستدعي `SetApprovalGate()`؛ `internal/posting/engine.go` | ربط Approval Engine بالـ Posting Engine وفق إعداد سياسة صريح، ومنع الإنتاج دون سياسة محددة. |
| GOV-06 | Maker-Checker | مطبق جزئياً | P1 | `internal/governance/approval.go`, `sod_policy.go`, `posting/engine.go` | اختبار جميع حالات نفس المنشئ/الموافق وإعادة استخدام الموافق عبر خطوات متعددة. |
| GOV-07 | Append-only Audit Log | مطبق على مستوى التخزين | P1 | `internal/governance/audit_chain.go`; `storage/postgres/governance_store.go`; `schema.sql` | استكمال تغطية العمليات وربطها بمسار المعاملات المالية. |
| GOV-08 | Hash Chain / Signatures / WORM | مطبق جزئياً | P1 | `audit_chain.go`, `verifier.go`, `worm_local.go`, `worm_s3.go` | اختبار التلاعب، وفشل التخزين الخارجي، وإدارة المفاتيح ودورانها واستعادة السجل. |
| GOV-09 | اكتمال Audit Coverage | جزئي | P0 | `internal/posting/engine.go` مقابل `governance/` | إنشاء مصفوفة أحداث تدقيق لكل Use Case وربطها بالـ Transaction. |

## G. التقارير والتقييم والتوحيد

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| REP-01 | ميزان المراجعة والأستاذ العام والقوائم | مطبق جزئياً | P1 | `internal/reporting/trial_balance.go`, `general_ledger.go`, `balance_sheet.go`, `income_statement.go`, `cash_flow.go` | بناء Dataset مرجعي ومقارنة النتائج مع حسابات مستقلة موثقة. |
| REP-02 | واجهات التقارير المحددة بالشركة | جزئي | P0 | `internal/reporting/*` تحتوي على `GenerateForCompanyContext()`؛ `engine.go` يقدم بعض الدوال العامة غير المحددة بالشركة | إضافة واجهات Facade محددة بالشركة وContext، وإيقاف المسارات العامة غير الآمنة مع RLS. |
| REP-03 | اتساق ميزان المراجعة مع الأستاذ العام | مطبق جزئياً | P1 | `internal/reporting/reporting_test.go`, `phase1_test.go` | إضافة اختبارات متعددة العملات والفترات والتسويات والعكس. |
| REP-04 | قائمة التدفقات النقدية | يحتاج تحققاً محاسبياً | P1 | `internal/reporting/cash_flow.go` | اختبار التصنيف التشغيلي/الاستثماري/التمويلي وحالات البنود غير النقدية وفق IAS 7. |
| REP-05 | الترجمة متعددة العملات | جزئي | P1 | `internal/reporting/currency_translation.go`, `internal/valuation/` | اختبارات أسعار الإقفال والمتوسط والتاريخي وفروق الترجمة. |
| REP-06 | Consolidation | جزئي | P1 | `internal/consolidation/`, `internal/reporting/consolidated_bundle.go` | استكمال سيناريوهات السيطرة وتغير الملكية وفقدان السيطرة وربط IFRS 3 عند الاقتضاء. |
| REP-07 | إعادة إنتاج التقرير تاريخياً | غير مثبت | P2 | `internal/reporting/`, `masterdata/account_version.go` | حفظ إصدار الحسابات والسياسات وأسعار الصرف ومعلمات التقرير المستخدمة في كل إصدار. |

## H. IFRS / IAS

> التقييم هنا يخص وجود أساس برمجي، ولا يعني إقرار توافق كامل مع المعيار. يجب بناء مصفوفة متطلبات رسمية لكل معيار وفق النصوص السارية.

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| IFRS-09 | IFRS 9 – ECL | جزئي | P1 | `internal/ifrs9/ecl.go`, `ecl_test.go` | توثيق نطاق المنهج المبسط، وإكمال المدخلات المستقبلية والسيناريوهات والنماذج اللازمة بحسب نوع الأداة. |
| IFRS-15 | IFRS 15 – Revenue | جزئي | P1 | `internal/ifrs15/revenue.go`, `revenue_test.go`; migration `202611100008` | تغطية المقابل المتغير وتعديلات العقود والتمويل الجوهري وطرق قياس التقدم والحالات الاستثنائية. |
| IFRS-16 | IFRS 16 – Leases | جزئي | P1 | `internal/ifrs16/lease.go`, `entries.go`, tests | توثيق convention معدل الخصم، ودعم تواريخ التدفقات الفعلية وإعادة القياس والتعديلات والاستثناءات. |
| IAS-12 | IAS 12 – Income Taxes | جزئي | P1 | `internal/ias12/ias12.go`, `journal.go`, tests | إضافة حالات الفروق المؤقتة المعقدة وتغير المعدلات والاعتراف والعرض والإفصاح. |
| IFRS-10 | IFRS 10 – Consolidation | جزئي | P1 | `internal/consolidation/`, `consolidated_bundle.go` | اختبار تحديد السيطرة وتغير نسب الملكية وNCI وفقدان السيطرة وربط الاستحواذ مع IFRS 3. |
| IFRS-AUTO | أتمتة تشغيل وظائف IFRS | جزئي / غير مكتمل في CLI | P1 | `internal/ifrsautomation/jobs.go`, `service.go`; `cmd/agy/main.go` | التحقق من تسجيل Jobs فعلياً في CLI، وعدم تجاهل أخطاء `RunPeriod()` في Daemon. |

## I. الاختبارات والتحقق

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| TEST-01 | Unit Tests للقواعد الأساسية | مطبق | P1 | اختبارات `internal/*` | المحافظة على اختبارات القواعد وإضافة حالات حدودية. |
| TEST-02 | PostgreSQL Integration Tests | غير مثبت بالكامل | P0 | `storage/postgres/*_test.go`; `docs/testing_confidence_report.md` | تشغيل الاختبارات على قاعدة نظيفة ومعالجة تعارض Checksum للترحيل `202611100008`. |
| TEST-03 | Concurrency Tests على Mock | مطبق جزئياً | P1 | `internal/posting/engine_test.go` | الاحتفاظ بها، لكنها لا تستبدل اختبار PostgreSQL. |
| TEST-04 | Concurrency Tests على PostgreSQL لنفس اليومية | غير مثبت | P0 | `storage/postgres/benchmark_test.go`; `docs/testing_confidence_report.md` | اختبار عدة عمال على اليومية نفسها، والتحقق من التسلسل والـ deadlocks وإعادة المحاولة. |
| TEST-05 | Fault Injection داخل Transaction | غير مكتمل | P0 | `storage/postgres/phase2_subledger_test.go`; `docs/testing_confidence_report.md` | محاكاة فشل كتابة بعد بدء المعاملة أو قطع الاتصال، والتحقق من Rollback كامل. |
| TEST-06 | Idempotency على التخزين الدائم وبالتزامن | جزئي | P1 | `internal/subledgers/subledgers_test.go`; `event_bridge.go` | اختبار إعادة التشغيل والطلبات المتزامنة واختلاف Payload لنفس المفتاح. |
| TEST-07 | Tenant Isolation على PostgreSQL | جزئي | P0 | `storage/postgres/rls_test.go` | اختبار قراءة وكتابة وترحيل عابر للشركات باستخدام دور التطبيق. |
| TEST-08 | Golden Accounting Test Data | غير مطبق بما يكفي | P1 | `internal/reporting/*_test.go`, `internal/ifrs*/` | بناء بيانات مرجعية موثقة ومقارنة مستقلة للقوائم. |
| TEST-09 | Property-Based Tests | غير مثبت | P2 | `internal/types/money_test.go`, `posting/engine_test.go` | إضافة توليد عشوائي لقيود متوازنة وتحويلات وتقريب وعكس. |
| TEST-10 | CI يمنع تخطي اختبارات حرجة بصمت | غير مثبت | P1 | `docs/testing_confidence_report.md` | جعل PostgreSQL tests إلزامية في CI مع فشل صريح عند Skip غير متوقع. |

## J. قاعدة البيانات والتشغيل

| ID | المعيار | الحالة | الأولوية | مواضع التنفيذ / الدليل | المطلوب |
| --- | --- | --- | --- | --- | --- |
| DB-01 | Migrations مرقمة وقابلة للتراجع | مطبق جزئياً | P1 | `storage/postgres/migrations/`, `migrations.go` | معالجة ازدواجية تشغيل `schema.sql` مع نظام Migrations وتحديد مصدر الحقيقة للمخطط. |
| DB-02 | Constraints وForeign Keys | مطبق جزئياً جيداً | P1 | `storage/postgres/schema.sql`, `migrations/*.sql` | مراجعة فجوات القيود التي لا تزال مفروضة في Go فقط. |
| DB-03 | Materialized/Perpetual Balances | مطبق جزئياً | P0 | migration `202611100009_perpetual_account_balances.sql`; `storage/postgres/perpetual_ledger_test.go` | توفير إعادة بناء ومطابقة ومعالجة انحراف الرصيد. |
| DB-04 | Observability | جزئي | P2 | `storage/postgres/tracing.go`, `tracing_test.go` | توحيد Logs/Metrics/Tracing وربطها بـ Company ID وEntry ID دون تسريب بيانات حساسة. |
| OPS-01 | النسخ الاحتياطي والاستعادة | غير مثبت في المستودع | P1 | لا يظهر مسار تشغيل متكامل ضمن ملفات المحرك | إضافة Runbook واختبار Restore دوري موثق. |
| OPS-02 | ترقية قاعدة البيانات بأمان | جزئي | P1 | `storage/postgres/migrations.go` | اختبار Upgrade من إصدار سابق على بيانات فعلية مصطنعة، مع خطة Rollback/Forward Recovery. |
| OPS-03 | جاهزية التشغيل متعدد النسخ | غير مثبت بالكامل | P1 | State داخلي في `EventBridge`, `Scheduler`, `ApprovalEngine` | اختبار نسختين أو أكثر من الخدمة ضد قاعدة مشتركة. |

---

# 4. قائمة الإصلاح ذات الأولوية

## المرحلة 0 — إغلاق موانع الإنتاج (P0)

- [ ] **P0-01:** تعديل Trigger توازن القيود متعددة العملات ليستخدم المبالغ الوظيفية.
- [ ] **P0-02:** توحيد سياسة الدقة والتقريب بين Money وPostgreSQL والتقارير.
- [ ] **P0-03:** إيقاف/إصلاح `Driver.PostBulk()` حتى يطبق التسلسل والتحقق والضمانات نفسها.
- [ ] **P0-04:** منع استخدام `GetNextSequenceWithLock()` كحجز تسلسل؛ إضافة قفل/تحديث ذري أو حصر التخصيص في `PostAndSequenceContext()`.
- [ ] **P0-05:** فرض مسار Posting واحد لا يمكن تجاوزه من واجهات التطبيق.
- [ ] **P0-06:** ربط الموافقة بالقيد المحفوظ ومبلغ العملة الوظيفية وبصمة محتواه.
- [ ] **P0-07:** تفعيل Approval Gate وفق إعداد إلزامي واضح في بيئة الإنتاج.
- [ ] **P0-08:** ربط Audit Trail بعمليات الترحيل والعكس والإقفال داخل حدود المعاملة.
- [ ] **P0-09:** إضافة اختبارات PostgreSQL تثبت عزل الشركات في عمليات الترحيل.
- [ ] **P0-10:** إضافة مقارنة آلية بين أرصدة GL المخزنة والأرصدة المعاد احتسابها.

## المرحلة 1 — الاعتمادية والدفاتر المساعدة (P1)

- [ ] **P1-01:** جعل Idempotency يتحقق من Payload Hash.
- [ ] **P1-02:** حماية عمليات Fixed Assets من تكرار Disposal/Impairment.
- [ ] **P1-03:** إكمال الذرية بين GL وSubledger.
- [ ] **P1-04:** توحيد خدمات Subledgers مع Company Scope.
- [ ] **P1-05:** إصلاح واجهات التقارير العامة لتكون Company-Scoped وContext-aware.
- [ ] **P1-06:** استكمال RBAC وAuthorization في Use Cases.
- [ ] **P1-07:** استكمال مطابقة AR/AP/Assets/Treasury مع حسابات الرقابة.
- [ ] **P1-08:** استكمال اختبار التزامن والفشل على PostgreSQL.
- [ ] **P1-09:** معالجة Migration Checksum وتشغيل كل اختبارات التكامل.
- [ ] **P1-10:** مراجعة وظائف IFRS Automation وتسجيل Jobs ومعالجة الأخطاء.

## المرحلة 2 — المعمارية وقابلية التوسع (P2)

- [ ] **P2-01:** تفكيك `StorageDriver` إلى Ports متخصصة.
- [ ] **P2-02:** إزالة `any` من Constructor العام أو توثيق بدائل آمنة.
- [ ] **P2-03:** تمرير Context عبر جميع مسارات Subledger.
- [ ] **P2-04:** تحديد State الدائم مقابل Cache في كل Module.
- [ ] **P2-05:** إضافة Property-Based Tests.
- [ ] **P2-06:** إعداد ADR لكل قرار معماري مؤثر.
- [ ] **P2-07:** تحسين Reproducibility للتقارير التاريخية.

## المرحلة 3 — اكتمال المعايير المحاسبية (P1/P2)

- [ ] **IFRS-01:** إعداد Compliance Matrix رسمية لكل معيار مطبق.
- [ ] **IFRS-02:** ربط كل متطلب بقاعدة محاسبية ووظيفة واختبار.
- [ ] **IFRS-03:** توثيق حدود التغطية والحالات غير المدعومة.
- [ ] **IFRS-04:** بناء Golden Cases بمراجعة محاسب مؤهل.
- [ ] **IFRS-05:** اختبار التقارير والإفصاحات مع سيناريوهات معيارية.

---

# 5. بوابة قبول أي Pull Request

لا يقبل أي تغيير في المحرك قبل تعبئة البنود ذات الصلة:

- [ ] هل يلتزم التغيير بحدود المسؤوليات؟
- [ ] هل يحافظ على صحة القيد المزدوج والعملات؟
- [ ] هل يحافظ على Company Scope؟
- [ ] هل يمر عبر Posting Authority عند التأثير في GL؟
- [ ] هل يحترم Transaction Boundaries؟
- [ ] هل يمنع التكرار عند الحاجة؟
- [ ] هل يعالج التزامن؟
- [ ] هل يسجل Audit Event عند الحاجة؟
- [ ] هل توجد اختبارات نجاح وفشل؟
- [ ] هل توجد Regression Tests؟
- [ ] هل تم تحديث Migrations والوثائق؟
- [ ] هل تم توثيق أي استثناء من معيار MUST؟

---

# 6. قواعد تحديث هذه المصفوفة

1. لا تغير الحالة إلى **مطبق** لمجرد إضافة كود؛ يجب أن يوجد اختبار يثبت السلوك.
2. لا تغير الحالة إلى **مطبق** إذا كان الاختبار يتخطى التنفيذ في بيئة PostgreSQL المطلوبة.
3. عند إصلاح بند، أضف رابط/مسار الاختبار الذي يثبت الإصلاح.
4. إذا تغير اسم الملف أو نقلت الوظيفة، حدّث مسار الدليل.
5. إذا كان المتطلب يعتمد على سياسة محاسبية، سجّل اسم السياسة وإصدارها ومصدرها.
6. أعد تقييم جميع البنود المتأثرة بعد أي تغيير في Posting أو Money أو Schema.
7. احتفظ بسجل تغييرات للمصفوفة يتضمن التاريخ والسبب والمسؤول.

## سجل التغييرات

| الإصدار | التاريخ | التغيير |
|---|---|---|
| 1.0 | 2026-11-13 | إنشاء أول مصفوفة مطابقة مرتبطة بمستودع Accounting Engine المرفق. |

---

# 7. حدود هذا التقييم

- هذه المصفوفة مبنية على فحص ملفات المصدر والاختبارات والوثائق داخل الأرشيف المرفق.
- لا تعني أن جميع الاختبارات أعيد تشغيلها في بيئة PostgreSQL مستقلة.
- تقرير `docs/testing_confidence_report.md` داخل المشروع يذكر أن بعض اختبارات PostgreSQL تم تخطيها بسبب اختلاف Checksum للترحيل `202611100008`.
- لا تمثل هذه الوثيقة رأياً تدقيقياً أو شهادة امتثال IFRS أو ISO.
- يجب أن يراجع مختص محاسبي مؤهل قواعد القياس والاعتراف والعرض والإفصاح قبل اعتمادها إنتاجياً.

**المبدأ الحاكم:** لا يُغلق أي بند حرج بالاعتماد على وجود الكود فقط؛ يجب إثباته باختبار قابل للتكرار ودليل واضح.
