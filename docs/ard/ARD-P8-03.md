# ARD-P8-03: استبدال `sync.Mutex` العام بتزامن على مستوى قاعدة البيانات وأقفال دقيقة

- **المهمة:** `P8-03`
- **المرحلة:** `P8`
- **تاريخ الإتمام:** 2026-11-17
- **حالة المهمة:** منفذة.
- **الاعتماديات:** لا شيء (مستقلة داخل `P8`).
- **الأدلة الرئيسية:**
  - استراتيجية القفل الداخلية: [`internal/posting/lock.go`](../../internal/posting/lock.go)
  - قفل PostgreSQL: [`internal/adapters/postgres/transactions.go`](../../internal/adapters/postgres/transactions.go)
  - قفل الذاكرة: [`internal/adapters/memory/posting_lock.go`](../../internal/adapters/memory/posting_lock.go)
  - العقد العام: [`pkg/spi/storage/posting_capabilities.go`](../../pkg/spi/storage/posting_capabilities.go)
  - اختبارات التزامن: [`test/concurrency/posting_lock_test.go`](../../test/concurrency/posting_lock_test.go)
  - اختبار تكامل PostgreSQL: [`internal/adapters/postgres/posting_lock_integration_test.go`](../../internal/adapters/postgres/posting_lock_integration_test.go)

---

## 1. السياق والقرارات المعمارية (Context & Architectural Decisions)

1. **المشكلة:**
   - كان `internal/posting/engine.go` يستخدم `sync.Mutex` واحداً لكل المحرك.
   - النتيجة: تسلسل كل عمليات الترحيل حتى للدفاتر والشركات المختلفة، وتحويل المحرك إلى عنق زجاجة، مع غياب أي حماية بين عدة نسخ (Multi-instance) لأن القفل محلي للعملية فقط.

2. **مفتاح القفل:**
   - تم اختيار `(CompanyID, JournalID)` كوحدة قفل.
   - هذا يمنع تعارض تخصيص التسلسل داخل نفس الدفتر، ويسمح بالترحيل المتوازي الحقيقي بين الدفاتر المنفصلة والشركات المنفصلة.

3. **واجهة `PostingLocker`:**
   - العقد الداخلي في `internal/posting`:
     ```go
     type PostingLocker interface {
         WithinPostingLock(ctx context.Context, companyID, journalID uuid.UUID, fn func(context.Context) error) error
     }
     ```
   - العقد العام الموازي في `pkg/spi/storage` يستخدم أنواع `pkg/api/v1/types` حتى لا يتسرب أي نوع داخلي.

4. **البديل الافتراضي في الذاكرة:**
   - `KeyedPostingLocker` يحتفظ بخريطة `map[(company,journal)]*postingLockEntry` مع عدّاد مراجع.
   - عند وصول عدد المراجع إلى صفر يُحذف المدخل، فلا تتراكم الأقفال بلا حد.
   - هذا بديل دقيق (fine-grained) وليس محاكاة لقفل عام.

5. **قفل PostgreSQL:**
   - يستخدم `pg_advisory_xact_lock(hashtextextended($1, 0))` حيث المفتاح `companyID:journalID`.
   - القفل مرتبط بالمعاملة (transaction-scoped)، فيُحرَّر تلقائياً عند `COMMIT` أو `ROLLBACK`.
   - عند وجود معاملة نشطة في السياق يُكتسب القفل داخلها مباشرة؛ وإلا تُفتح معاملة جديدة عبر `ExecuteWithOptions`.
   - يتحقق المحول من تطابق شركة المعاملة مع الشركة المطلوبة قبل اكتساب القفل.

6. **الترحيل الجماعي (Bulk):**
   - `WithinPostingLocks` يجمع مفاتيح الدفاتر، يزيل التكرار، ويرتبها نصياً ثم يكتسبها بالتسلسل.
   - الترتيب الحتمي يمنع الجمود (Deadlock) بين عمليتين تشملان عدة دفاتر بترتيب مختلف.

7. **التوصيل عبر Public SPI:**
   - `storage.PostingCapabilities.Locker` و`storage.PostingLockerExtensionKey` يتيحان للمحول تمرير استراتيجيته.
   - `sdkapplication` يربط الواجهة العامة بالواجهة الداخلية عبر `publicPostingLocker`.
   - عند غياب استراتيجية من المحول يستخدم المحرك `KeyedPostingLocker` كبديل آمن.

---

## 2. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| إزالة القفل العام الأحادي من مسار الترحيل | لم يعد `posting.Engine` يحمل `sync.Mutex`؛ كل المسارات تستخدم `e.postingLocker()` أو `WithinPostingLocks` | **محقق** |
| الترحيل المتزامن في دفاتر مختلفة يعمل بالتوازي على PostgreSQL | `TestKeyedPostingLocker_SerializesSameJournalOnly` يثبت عدم تسلسل الدفاتر المختلفة؛ واختبار التكامل يشغّل ترحيلاً متزامناً على دفاتر منفصلة | **محقق** |
| الترحيل على نفس الدفتر يتسلسل دون Deadlocks أو فجوات ترقيم | `TestPostingConcurrency_SameJournalKeepsContiguousSequences` يتحقق من تسلسل الأرقام وتكاملها | **محقق** |
| تحرير القفل تلقائياً عند Rollback | قفل PostgreSQL من نوع `pg_advisory_xact_lock` يُحرَّر مع نهاية المعاملة؛ اختبار التكامل يغطي فشل المعاملة ثم إعادة المحاولة | **محقق** |
| محاكاة دقيقة ومكافئة في محول الذاكرة | `memory.Driver.WithinPostingLock` يستخدم `KeyedPostingLocker` بنفس مفتاح `(company, journal)` | **محقق** |

---

## 3. ملاحظات تشغيلية

- **لا قفل عبر الشبكة في الذاكرة:** محول الذاكرة يظل محلياً للعملية؛ هذا موثق كقيد في سياسة التوافق ولا يُستخدم للإنتاج.
- **مفاتيح القفل:** `hashtextextended` ينتج `bigint`؛ تصادم نظري ممكن لكنه لا يسبب سوى تسلسل إضافي بين دفترين، ولا يسبب فساداً.
- **الترتيب الحتمي:** أي عملية تمس عدة دفاتر يجب أن تستخدم `WithinPostingLocks` لا استدعاءات متداخلة يدوية، لتجنب الجمود.
- **المراقبة:** زيادة زمن الانتظار على نفس الدفتر مؤشر طبيعي على التسلسل المقصود، وليست خطأً.
