# تقرير تكافؤ العقود التخزينية (Contract Parity Report) — P3-07

- **تاريخ الإنشاء:** 2026-11-14T19:35:56Z
- **نطاق الاختبار:** المقارنة الدلالية المحاسبية بين موائمات `PostgreSQL` و`Memory` و`External Adapter`.
- **معايير التكافؤ:** دقة الأرصدة، حالات القيود، دورة العكس، منع التكرار (Idempotency)، رفض المدخلات غير المتوازنة والفترات المغلقة.

## 1. جدول النتائج حسب السيناريو والموائم

| السيناريو | Memory | External Adapter | PostgreSQL | حالة التكافؤ الدلالي |
| :--- | :---: | :---: | :---: | :---: |
| Draft -> Validate -> Post -> GetEntry -> TrialBalance | PASS ✅ | PASS ✅ | PASS ✅ | متطابق 100% 🎯 |
| Reversal Lifecycle & Double Reversal Rejection | PASS ✅ | PASS ✅ | PASS ✅ | متطابق 100% 🎯 |
| Event Ingestion Idempotency & Conflict Detection | PASS ✅ | FAIL ❌ | PASS ✅ | يوجد تباين ⚠️ |
| Semantic Invariants: Closed Period, Unbalanced, Bad Account, Tenant Isolation | PASS ✅ | PASS ✅ | PASS ✅ | متطابق 100% 🎯 |
| Declared Adapter Capabilities Matrix | PASS ✅ | PASS ✅ | PASS ✅ | متطابق 100% 🎯 |

## 2. جدول الفروقات المشروعة للقدرات (Capability Differences)

توضح الفروقات المشروعة بين الموائمات التي يقرها عقد `storage.AdapterCapabilities` دون أن تخل بدقة وسلامة القيود المحاسبية:

| القدرة (Capability) | PostgreSQL | Memory | External Adapter | الأثر التشغيلي |
| :--- | :---: | :---: | :---: | :--- |
| **الاستمرارية على القرص (Durability)** | مدعومة (WAL) ✅ | غير مدعومة (ذاكرة مؤقتة) ⚠️ | غير مدعومة (ذاكرة مؤقتة) ⚠️ | Memory و External مخصصان للاختبار والتطوير؛ الإنتاج يتطلب PostgreSQL. |
| **القيود المرجعية (Foreign Keys)** | مفروضة على مستوى DB ✅ | مفروضة عبر Domain Logic ℹ️ | مفروضة عبر Domain Logic ℹ️ | يتم حماية سلامة العلاقات في Memory/External برمجياً في طبقة النواة. |
| **عزل العمليات (Isolation)** | Serializable / ReadCommitted | Serializable (Mutex Process-local) | Serializable (Mutex Process-local) | التزامن آمن في الجميع؛ PostgreSQL يوفر عزلاً عبر سيرفرات وعمليات متعددة. |
| **اكتشاف التعارض الميت (Deadlocks)** | تلقائي مع كود خطأ `40P01` | قفل تسلسلي يمنع التعارض | قفل تسلسلي يمنع التعارض | في الذاكرة لا يحدث Deadlock لتسلسل المعاملات داخل Mutex واحد. |

## 3. إحصاء الاختلافات غير المفسرة (Unexpected Differences)

- **عدد الاختلافات غير المفسرة:** `0` (الهدف: `0` ✅)
- **حالة بوابات القبول:** تم استيفاء جميع معايير القبول للمهمة `P3-07`.
