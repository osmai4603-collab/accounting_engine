# ARD-P8-04: ضمان ذرية الترحيل عبر `storage.Ports` (Atomic Posting Boundary)

- **المهمة:** `P8-04`
- **المرحلة:** `P8`
- **تاريخ الإتمام:** 2026-11-17
- **حالة المهمة:** منفذة.
- **الاعتماديات:** `P8-01`.
- **الأدلة الرئيسية:**
  - حدود الترحيل الذرية: [`internal/portbridge/repository.go`](../../internal/portbridge/repository.go)
  - عقد وحدة العمل والأخطاء: [`pkg/spi/storage/transaction.go`](../../pkg/spi/storage/transaction.go)
  - كشف التداخل في PostgreSQL: [`internal/adapters/postgres/transactions.go`](../../internal/adapters/postgres/transactions.go)
  - كشف التداخل في الذاكرة: [`internal/adapters/memory/unit_of_work.go`](../../internal/adapters/memory/unit_of_work.go)
  - اختبارات حقن الفشل: [`test/atomic/posting_atomicity_test.go`](../../test/atomic/posting_atomicity_test.go)

---

## 1. السياق والقرارات المعمارية (Context & Architectural Decisions)

1. **المشكلة:**
   - على المسار الحديث كان تخصيص التسلسل (`NextSequence`) و`CommitPosting` قابلين للانفصال.
   - فشل بينهما يعني إما رقماً محروقاً أو قيداً بلا أثر محاسبي كامل، وهو تهديد مباشر لسلامة دفتر الأستاذ.

2. **وحدة عمل واحدة (Single Unit of Work):**
   - `Repository.PostAndSequenceContext` أصبح يفتح `Ports.UOWRunner.Execute(...)` ويُنفّذ داخلها:
     1. `uow.Sequences().NextSequence(...)` — تخصيص التسلسل.
     2. `uow.Posting().CommitPosting(...)` — الرأس والسطور والحالة والأرصدة والتدقيق وعدم التكرار حسب عقد المحول.
   - أي خطأ يعيده أي منهما يلغي المعاملة كاملة، فلا يبقى أثر جزئي.

3. **التعامل مع التداخل (Nested UoW):**
   - قفل الترحيل في PostgreSQL يفتح معاملة خاصة به (`WithinPostingLock`).
   - لذلك لا يجوز أن يفتح `PostAndSequenceContext` معاملة ثانية داخل السياق نفسه.
   - أضيف الخطأ المعياري `storage.ErrNestedUnitOfWork`، ويكشفه المحولان عند وجود معاملة نشطة.
   - عند استلامه يعيد `PostAndSequenceContext` استخدام منافذ المعاملة النشطة (`Ports.Sequences` و`Ports.Posting`) بدلاً من فتح معاملة جديدة.

4. **مسار احتياطي صريح:**
   - عندما لا يوفّر المحول `UOWRunner`، يُخصَّص التسلسل ثم يُستدعى `CommitPosting` مباشرة.
   - هذا المسار محتفظ به للتوافق مع المحولات القديمة، لكنه لا يقدّم ضمان الذرية الكامل؛ المحولات الحديثة يجب أن توفّر `UOWRunner`.

5. **سياق الشركة والفاعل:**
   - يُلحق `PostAndSequenceContext` سياق الشركة (`WithCompanyContext`) وسياق الفاعل (`WithActorContext`) قبل فتح وحدة العمل، ليبقى عزل RLS وتتبع التدقيق صحيحاً.

6. **التحقق من النتيجة:**
   - `validateCommittedEntry` يعمل داخل المعاملة أيضاً؛ أي عدم تطابق بين القيد المطلوب والنتيجة يُبطل المعاملة بدلاً من حفظ قيد مشوّه.

---

## 2. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| جميع خطوات الترحيل داخل معاملة ذرية واحدة | `TestAtomicPosting_SuccessUsesSingleUnitOfWork` يثبت أن تنفيذ وحدة العمل يحدث مرة واحدة فقط | **محقق** |
| فشل أي خطوة وسيطة يؤدي إلى Rollback كامل دون آثار جزئية | `TestAtomicPosting_FailureAfterSequenceRollsBackAndDoesNotBurnNumber` يحقن فشلاً في `CommitPosting` بعد تخصيص التسلسل ويتحقق من عدم بقاء القيد ومن عدم استهلاك الرقم | **محقق** |
| نجاح اختبارات حقن الفشل بعد التخصيص وقبل التثبيت | `TestAtomicPosting_ConcurrentFailuresNeverLeavePartialState` يشغّل فشلاً متزامناً ويتحقق من الحالة النهائية | **محقق** |
| توثيق معايير الذرية كمرجع إلزامي لـ `P9-01` | هذا الـ ARD + [`docs/architecture/unit_of_work_and_transactions.md`](../architecture/unit_of_work_and_transactions.md) يحددان حدود المعاملة كمرجع للإقفال السنوي | **محقق** |
| تكافؤ السلوك في Contract Parity | `TestContractParity_CoreLifecycle` و`TestContractParity_CrossAdapterSemanticEquivalence` يمران بعد ربط `external_adapter.go` بمنافذ معاملة لا تعيد قفل المتجر (تخصيص المسلسل والترحيل داخل نفس القفل) | **محقق** |

---

## 3. حدود المسؤولية بين المحرك والمحول

- **المحرك:** يضمن أن التخصيص والترحيل يُطلبان داخل وحدة العمل نفسها، وأن أي خطأ يوقف الكل.
- **المحول:** يضمن أن `UnitOfWork` الممرَّرة ملتزمة فعلياً (معاملة قاعدة بيانات واحدة)، وأن `Sequences()` و`Posting()` يكتبان في نفس المعاملة.
- **الذاكرة:** تنفذ المعاملة على لقطة منفصلة وتُثبّتها فقط عند النجاح، مع كشف التداخل بنفس الخطأ المعياري.

---

## 4. شرط مسبق للمرحلة `P9`

أي مهمة في `P9` (وخاصة الإقفال السنوي `P9-01`) يجب أن تفترض:

1. لا تُكتب أي حركة محاسبية خارج وحدة عمل.
2. كل أثر جانبي (تدقيق، عدم تكرار، أرصدة) جزء من نفس المعاملة.
3. الفشل بعد تخصيص التسلسل لا يحرق الرقم ولا يترك قيداً معلقاً.
4. التداخل يُرفض صراحةً بـ `storage.ErrNestedUnitOfWork` ولا يُعالَج بمعاملة ثانية صامتة.

---

## 5. المتابعة

- إضافة تغطية صريحة لمسار عدم التكرار (`WithPostingIdempotency`) داخل نفس وحدة العمل عند اعتماد المساعد في العقد العام.
- توسيع اختبارات حقن الفشل لتشمل فشل التدقيق وفشل تحديث الأرصدة بشكل منفصل لكل محول.
