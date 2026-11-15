# ARD-P4-07: مراجعة Financial Reporting وسلامة القوائم والمصالحة والتتبع

- **المهمة:** `P4-07`
- **المرحلة:** `P4`
- **تاريخ الإتمام:** 2026-11-16
- **الحالة عند الإغلاق:** منفذة
- **النطاق:** `internal/reporting/statement_reconciliation.go`، `internal/reporting/statement_reconciliation_test.go`، `internal/reporting/engine.go`، ووثيقة السياسة `docs/accounting/financial_reporting_scope.md`.

---

## 1. السياق والمشكلة (Context & Problem)

مجرد وجود استعلامات لميزان المراجعة (TB)، الأستاذ العام (GL)، وقائمتي المركز المالي (BS) والدخل (IS) لا يكفي لضمان:

1. تصنيف سليم وموثق لجميع الحسابات ضمن شجرة المعايير المحاسبية (Taxonomy).
2. مصالحة رياضية ومحاسبية دقيقة بين ميزان المراجعة وقائمة الدخل وقائمة المركز المالي وحركة حقوق الملكية.
3. منع إخفاء الحسابات غير المصنفة أو المعلقة أو تسوية الفروقات بالتقريب الصامت.
4. اكتشاف الأرصدة غير الطبيعية (Abnormal Balances) التي قد تشير إلى أخطاء ترحيل أو عمليات سحب على المكشوف أو مدفوعات زائدة.
5. توثيق نطاق الإفصاح وحدود التقارير والتأكيد على عدم استخدام وصف «قوائم مالية كاملة متوافقة مع IFRS» لقوائم تفتقر للإيضاحات المتممة المملوكة للنظام المضيف.
6. قابلية تتبع الأرقام الإجمالية نزولاً إلى أسطر اليومية وأحداث المعاملات الاقتصادية المصدرية (Traceability).

---

## 2. القرارات والتنفيذ (Decisions & Implementation)

1. **محرك مصالحة القوائم المالية (`StatementReconciler`):**
   - تم تطوير `StatementReconciler` في `internal/reporting/statement_reconciliation.go` لربط وفحص مخرجات القوائم المالية آلياً:
     - التحقق من توازن ميزان المراجعة بدقة مطلقة (`TBDebitTotal == TBCreditTotal`).
     - التحقق من توازن الميزانية العمومية (`Total Assets == Total Liabilities + Total Equity`).
     - التحقق من تطابق صافي ربح/خسارة الفترة المشتق من ميزان المراجعة مع صافي ربح قائمة الدخل (`TBNetIncome == ISNetIncome`).
     - مطابقة إجمالي حقوق الملكية وحركة صافي الدخل في قائمة التغير في حقوق الملكية مع رصيد حقوق الملكية في الميزانية العمومية.

2. **منع إخفاء الحسابات غير المصنفة (`Unclassified Accounts`):**
   - فحص كل حساب في نطاق التقرير؛ في حال وجود حساب بدون تصنيف أو بتصنيف فرعي ناقص، لا يُستبعد أو يُدمج سراً، بل يُسجل ضمن مصفوفة `UnclassifiedAccounts` موثقاً الكود والاسم والرصيد المدين والدائن وسبب الاستثناء، وتُعتبر المصالحة غير مكتملة (`Reconciled = false`).

3. **كشف الأرصدة غير الطبيعية (`Abnormal Balances Detector`):**
   - فحص أرصدة الحسابات ومقارنتها بطبيعتها المعتادة:
     - رصيد دائن في حساب أصول غير مقابل (Contra Asset).
     - رصيد مدين في حساب خصوم.
     - رصيد مدين في حساب إيرادات غير مقابل (Contra Revenue).
     - رصيد دائن في حساب مصروفات.
   - إبراز هذه الحالات فوراً كتحذيرات تدقيقية صريحة.

4. **مطابقة الفترات المقارنة (`Comparative Reconciliation`):**
   - إضافة دالة `ReconcileComparativePeriods` للتأكد رياضياً من تطابق الأرصدة الافتتاحية للفترة الحالية مع أرصدة الإقفال للفترة المقارنة السابقة بدون انقطاع أو انحراف.

5. **وثيقة سياسة وحدود التقارير المالية:**
   - إصدار وثيقة `docs/accounting/financial_reporting_scope.md` وتحديد نطاق مسؤوليات المحرك مقارنة بالنظام المضيف، وتأكيد قاعدة حظر تسمية التقارير «Full IFRS Financial Statements» دون حزمة الإفصاحات الكاملة.

---

## 3. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول في الخطة | الدليل الاختباري / التوثيقي | النتيجة |
| :--- | :--- | :--- |
| **TB متوازن وفق تعريف التقرير والعملة والفترة** | `TestStatementReconciler_EndToEndReconciliation` | محقق (`TrialBalanceBalanced == true`, `Debit == Credit`) |
| **كل مبلغ قابل للتتبع إلى GL ومصدره** | `internal/reporting/traceability.go` & `TestFinancialTraceabilityUsesCompanyScopedRequestAndContext` | محقق |
| **القوائم تتصالح مع الأستاذ العام وفق القواعد المعتمدة** | `TestStatementReconciler_EndToEndReconciliation` | محقق (`NetIncomeReconciled == true`, `EquityStatementTied == true`) |
| **لا تُخفى الحسابات غير المصنفة أو فروق التقريب** | `TestStatementReconciler_UnclassifiedAccountsExposedAndNeverHidden` | محقق (كشف الحساب 9999 برصيده الكامل وتجميد علامة Reconciled) |
| **نطاق التقارير مراجع محاسبيًا وموثق** | `docs/accounting/financial_reporting_scope.md` | محقق وموثق وفق متطلبات IAS 1 و IAS 21 |

---

## 4. التحقق والاختبارات

تم تنفيذ اختبارات الحزمة بنجاح تام 100%:

```bash
go test -v ./internal/reporting/ -run 'TestStatementReconciler'
```

النتيجة:

- `TestStatementReconciler_EndToEndReconciliation`: PASS
- `TestStatementReconciler_UnclassifiedAccountsExposedAndNeverHidden`: PASS
- `TestStatementReconciler_AbnormalBalancesDetected`: PASS
- `TestStatementReconciler_ComparativePeriodsTied`: PASS
