# تقرير التحقق الفني والمطابقة الميدانية لعيوب وفجوات `accounting_engine`

**تاريخ التحقق:** نوفمبر 2026  
**المرجع الأساسي:** وثيقة تحليل الفجوات والعيوب [`docs/engine_errors_analysis.md`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/docs/engine_errors_analysis.md)  
**المنهجية:** الفحص المباشر للكود المصدري (Static Code Analysis & Schema Inspection)، تتبع مسارات التنفيذ (Execution Path Tracing)، ومطابقة كل ادعاء مع أرقام الأسطر والوظائف الفعلية في المستودع.

---

## 1. الملخص التنفيذي والحكم العام

بعد الفحص الميداني الدقيق لجميع بنود تقرير الوكيل السابق عبر ملفات المشروع في Go وقاعدة بيانات PostgreSQL، خلصنا إلى النتيجة التالية:

> **النتيجة العامة:**  
> **جميع العيوب والفجوات الحرجة والمشاكل المعمارية المذكورة في تقرير الوكيل حقيقية بنسبة 100% وموجودة فعلياً في الكود البرمجي الحالي للمشروع.**  
> لم يكن أي من الادعاءات الحرجة زائفاً أو ناشئاً عن سوء فهم؛ بل كشف التحقق أن بعض الفجوات أشد خطورة مما وصفه التقرير الأصلي (مثل دالة `GetAccountByID` التي تفشل حتماً في PostgreSQL مسببة شللاً لأي تعديل في شجرة الحسابات، ودالة `GetNextSequenceWithLock` التي تقرأ بدون قفل ولا تزيد العداد، ومسار `PostBulk` الذي يحفظ القيود دون أرقام تسلسلية).

---

## 2. جدول التحقق الإجمالي ومطابقة الادعاءات

| # | المشكلة / الفجوة | مستوى الخطورة | النتيجة الميدانية | الملف المعني |
| --- | --- | :---: | :---: | --- |
| **3.1** | فحص توازن القيود في Trigger قاعدة البيانات بالعملات المختلفة | **Critical (P0)** | ✅ **مؤكد بالدليل** | [`storage/postgres/schema.sql`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L330-L363) |
| **3.2** | عدم اتساق دقة المبالغ بين الذاكرة (`FinancialScale=4`) وقاعدة البيانات (`MinorUnits`) | **Critical (P0)** | ✅ **مؤكد بالدليل** | [`internal/types/money.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/types/money.go#L13) |
| **3.3** | الترحيل الجماعي `PostBulk` يتجاوز التسلسل المحاسبي وخلل `GetNextSequenceWithLock` | **Critical (P0)** | ✅ **مؤكد بالدليل** | [`storage/postgres/posting.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/posting.go#L502) & [`masterdata.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/masterdata.go#L416) |
| **3.4** | نظام الموافقات غير مرتبط بمحتوى القيد ومبلغه الفعلي وغياب قيد التفرد | **Critical (P0)** | ✅ **مؤكد بالدليل** | [`internal/governance/approval.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/governance/approval.go#L131) |
| **3.5** | غياب التكامل التلقائي لسجل التدقيق (Audit Trail) مع عمليات الترحيل والماستر | **Critical (P0)** | ✅ **مؤكد بالدليل** | [`internal/posting/engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/posting/engine.go) & [`storage/postgres/posting.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/posting.go) |
| **4.1** | إعادة استخدام `IdempotencyKey` في `EventBridge` يعيد القيد القديم دون فحص التطابق | **High (P1)** | ✅ **مؤكد بالدليل** | [`internal/subledgers/event_bridge.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/event_bridge.go#L116) |
| **4.2** | دورة حياة الأصول الثابتة غير ذرية وتسمح بازدواجية مسودات الاستبعاد/التدني | **High (P1)** | ✅ **مؤكد بالدليل** | [`internal/subledgers/fixed_assets.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L640-L729) |
| **4.3** | تهيئة الدفاتر المساعدة بـ `NewService` بدلاً من النسخة المخصصة للشركة | **High (P1)** | ✅ **مؤكد بالدليل** | [`engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/engine.go#L153) |
| **4.4** | واجهات التقارير في الـ Facade لا تستقبل `CompanyID` في بيئة Multi-Tenant | **High (P1)** | ✅ **مؤكد بالدليل** | [`engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/engine.go#L73-L83) |
| **4.5** | `UpdateAccountParent` تفشل حتماً في PostgreSQL بسبب `GetAccountByID` غير المحددة بشركة | **High (P1)** | ✅ **مؤكد بالدليل** | [`internal/masterdata/service.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/masterdata/service.go#L106) |
| **4.6** | أمر CLI لأتمتة IFRS يشغل المجدول بدون أي وظائف مسجلة (`jobs`) ويتجاهل الأخطاء | **Medium (P2)** | ✅ **مؤكد بالدليل** | [`cmd/agy/main.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/cmd/agy/main.go#L78) |
| **5.0** | قصور محركات IFRS وتجريدها المفرط (IFRS 16, 9, 15, 10) | **Medium (P2)** | ✅ **مؤكد بالدليل** | [`internal/ifrs16/lease.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/ifrs16/lease.go#L152) & [`internal/ifrs9/ecl.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/ifrs9/ecl.go) |
| **6.2** | واجهة `StorageDriver` متضخمة تجمع 13 واجهة فرعية خارقة لمبدأ ISP | **Medium (P2)** | ✅ **مؤكد بالدليل** | [`engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/engine.go#L46-L60) |
| **6.3** | دالة البناء `New(first, second any)` تعتمد على `any` وفحص الأنواع وقت التشغيل | **Medium (P2)** | ✅ **مؤكد بالدليل** | [`engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/engine.go#L27-L44) |
| **6.4** | حرق `context.Background()` داخل عمليات الدفاتر المساعدة بدلاً من تمرير الـ Context | **Medium (P2)** | ✅ **مؤكد بالدليل** | [`internal/subledgers/fixed_assets.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L840) |
| **6.6** | إعداد `Config.BaseCurrency` شكلي ولا يدخل في أي عملية حسابية على الإطلاق | **Low (P3)** | ✅ **مؤكد بالدليل** | [`engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/engine.go#L20) |
| **7.1** | `EnsureSchema()` تنفذ ملف SQL خام وغير مرقم عند كل اتصال بجانب Migrations | **Medium (P2)** | ✅ **مؤكد بالدليل** | [`storage/postgres/driver.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L52-L66) |

---

## 3. التحقق التفصيلي مدعوماً بالكود وأرقام الأسطر

### 3.1 فحص توازن القيود في Trigger قاعدة البيانات (Critical - P0)

* **الموقع:** [`storage/postgres/schema.sql`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L330-L363)
* **الدالة:** `check_posted_move_balance_statement()`
* **الدليل الميداني:**
  في الأسطر 336-337 و 346:

  ```sql
  SELECT posted_moves.id,
         posted_moves.entry->>'Status' AS entry_status,
         COALESCE(SUM(lines.debit), 0) AS total_debit,        -- السطر 336
         COALESCE(SUM(lines.credit), 0) AS total_credit       -- السطر 337
  ...
  HAVING posted_moves.entry->>'Status' IS DISTINCT FROM 'posted'
      OR COALESCE(SUM(lines.debit), 0) <> COALESCE(SUM(lines.credit), 0)  -- السطر 346
  ```

  وفي الأسطر 192-193 من نفس الملف:

  ```sql
  COMMENT ON COLUMN account_move_lines.debit IS 'Deprecated compatibility column; stores functional-currency debit.';
  COMMENT ON COLUMN account_move_lines.credit IS 'Deprecated compatibility column; stores functional-currency credit.';
  ```

* **الأثر الحقيقي:**
  1. التحقق في قاعدة البيانات يعتمد على حقول مصنفة رسمياً في التعليقات بأنها **Deprecated**.
  2. الأعمدة الرسمية الصحيحة هي `functional_debit_minor` و `functional_credit_minor` من نوع `bigint`.
  3. استخدام `numeric(28,4)` في الـ Trigger بينما الحسابات التراكمية تعتمد على الـ Minor Units يُنشئ فجوة خطيرة بين ما يتحقق منه الـ Trigger وما تسجله القيود الفعلية.

---

### 3.2 عدم اتساق الدقة العشرية والتقريب (Critical - P0)

* **المواقع:**
  * [`internal/types/money.go:13`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/types/money.go#L13)
  * [`storage/postgres/posting.go:470-489`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/posting.go#L470-L489)
* **الدليل الميداني:**
  * في `types/money.go`:

    ```go
    const (
        FinancialScale = 4
        InternalScale  = 12
    )
    ```

    ويتم تقريب المبالغ في الذاكرة لـ 4 خانات عشرية (`RoundForPosting()`).
  * عند التخزين في PostgreSQL:
    يتم استدعاء `MinorUnits()` التي تطبق `RoundBank(currency.MinorUnit)` وتحول الرقم إلى `bigint` بعد إزاحته بمقدار خانات العملة الصغرى (مثلاً خانتين للـ USD/SAR وثلاث خانات للـ KWD).
* **الأثر الحقيقي:**
  المبالغ في طبقة الذاكرة والتقارير التي تقرأ الـ JSON تحتفظ بـ 4 خانات عشرية (مثل `100.2550`)، بينما مسار PostgreSQL وجدول `account_balances` المعتمد على الأرصدة التراكمية يخزنها بوحدات صغرى مقربة للخانة الثانية (مثل `10026`). هذا يولد اختلافات حتمية بين ميزان المراجعة المحسوب من القيود الأصلية والمحسوب من جدول الأرصدة المادية (Materialized Balances).

---

### 3.3 كوارث الترحيل الجماعي والتسلسل المحاسبي (Critical - P0)

* **المواقع:**
  * [`storage/postgres/posting.go:502-526`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/posting.go#L502-L526)
  * [`storage/postgres/masterdata.go:416-428`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/masterdata.go#L416-L428)
  * [`internal/posting/bulk.go:23-35`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/posting/bulk.go#L23-L35)
* **الدليل الميداني:**
  1. دالة `Driver.PostBulk()` في PostgreSQL:

     ```go
     func (d *Driver) PostBulk(ctx context.Context, entries []posting.JournalEntry) error {
         ...
         tx, err := d.beginCompanyTx(ctx, companyID)
         ...
         for _, entry := range entries {
             if err := d.saveEntryTx(ctx, tx, entry); err != nil {
                 return err
             }
         }
         return tx.Commit(ctx)
     }
     ```

     الدالة تحفظ القيود في Transaction واحدة لكنها **لا تخصص أرقاماً تسلسلية Sequence Numbers إطلاقاً ولا تحدث `current_sequence` في جدول اليوميات `journals`**! القيود ترحل بلا تسلسل رسمي.
  2. دالة `GetNextSequenceWithLock()` في `storage/postgres/masterdata.go`:

     ```go
     func (d *Driver) GetNextSequenceWithLock(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
         var sequence int64
         err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
             err := tx.QueryRow(ctx,
                 `SELECT current_sequence + 1 FROM journals WHERE company_id=$1 AND id=$2`,
                 companyID, journalID).Scan(&sequence)
             ...
             return err
         })
         return sequence, err
     }
     ```

     الدالة تحمل اسم `WithLock`، لكنها تنفذ مجرد `SELECT current_sequence + 1` عادي جداً **بدون `FOR UPDATE` وبدون إجراء `UPDATE` لزيادة العداد**! أي استدعاءين متزامنين سيحصلان على نفس الرقم بالضبط.
  3. دالة `Engine.PostBulk()` في طبقة الـ Domain:

     ```go
     func (e *Engine) PostBulk(entries []JournalEntry, posterID string) error {
         for i := range entries {
             ...
             if err := e.PostEntry(&entries[i], posterID); err != nil {
                 return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
             }
         }
         return nil
     }
     ```

     هنا الترحيل فردي وغير ذري إطلاقاً؛ إذا فشل القيد العاشر بعد نجاح تسعة، تظل التسعة السابقة مرحلة دون تراجع، خلافاً لما يتوقعه المستدعي من ترحيل دفعة جماعية (Batch Atomicity).

---

### 3.4 ثغرات نظام الموافقات والتحايل على الصلاحيات (Critical - P0)

* **المواقع:**
  * [`engine.go:142-164`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/engine.go#L142-L164)
  * [`internal/governance/approval.go:131-160`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/governance/approval.go#L131-L160)
  * [`storage/postgres/governance_store.go:524-533`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/governance_store.go#L524-L533)
  * [`storage/postgres/migrations/202611100005_phase3_governance.sql:56-67`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/migrations/202611100005_phase3_governance.sql#L56-L67)
* **الدليل الميداني:**
  1. في `engine.go:151`: يتم إنشاء المحرك كالتالي:
     `postingEngine := posting.NewEngine(driver, masterService)`
     ولا يتم استدعاء `postingEngine.SetApprovalGate(...)` إطلاقاً، ولا ضبط `MakerCheckerThreshold`. نظام الموافقات معطل افتراضياً في المحرك الرئيسي!
  2. في `internal/governance/approval.go:131`:
     `ApprovalEngine.Start(ctx, companyID, entryID, creatorID, journalType, amount, at)`
     تأخذ `amount` كمعامل حر يمرره المستدعي، **ولا تقرأ القيد من المخزن لتتأكد من مطابقة المبلغ للمبلغ الحقيقي للقيد**.
  3. في `storage/postgres/governance_store.go:524`:

     ```sql
     SELECT EXISTS(
         SELECT 1 FROM approval_requests
         WHERE company_id=$1 AND entry_id=$2 AND completed_at IS NOT NULL
     )
     ```

     دالة التحقق `IsEntryApproved` تكتفي فقط بوجود أي طلب موافقة مكتمل مرتبط بنفس المعرف، دون مطابقة مبلغ الموافقة أو بصمة القيد!
  4. في جدول `approval_requests`:
     القيد الفريد هو `UNIQUE (company_id, id)` فقط، ولا يوجد `UNIQUE (company_id, entry_id)`. يمكن إنشاء طلبات موافقة متكررة ومتضاربة لنفس القيد.

---

### 3.5 انفصال سجل التدقيق (Audit Trail) عن المعاملات المحاسبية (Critical - P0)

* **المواقع:**
  * [`internal/posting/engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/posting/engine.go)
  * [`storage/postgres/posting.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/posting.go)
* **الدليل الميداني:**
  عند البحث الشامل عن استدعاءات `AppendAuditLog` و `AppendAuditLogAtomic`، تبين أنها محصورة فقط داخل:
  * اختبارات الوحدة.
  * محرك سلسلة الكتل الخاص بالحوكمة (`audit_chain.go`).
  * تسجيل خطوة الموافقة (`RecordApproval`).
* **الأثر الحقيقي:**
  عمليات الترحيل اليومية (`PostEntry`), وعكس القيود (`ReverseEntry`), وتعديل دليل الحسابات, وإغلاق السنوات المالية تتم داخل قاعدة البيانات **دون توليد سجل تدقيق مالي بصورة تلقائية ذرية**. وجود الـ Hash Chain المتقدم في الحوكمة لا قيمة له طالما أن مسار الترحيل المحاسبي لا يمر عبره إجبارياً داخل المعاملة.

---

### 4.1 ثغرة تكرار مفتاح Idempotency في EventBridge (High - P1)

* **الموقع:** [`internal/subledgers/event_bridge.go:116-171`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/event_bridge.go#L116-L171)
* **الدليل الميداني:**

  ```go
  if entry, ok := b.entries[idempotencyKey]; ok {
      return entry, nil  // السطر 117: إعادة فورية دون فحص
  }
  ...
  if existing.found {
      return posting.JournalEntry{ID: existing.event.ResultEntryID, ...}, nil // السطر 132
  }
  ```

  عند إرسال حدث جديد بنفس المفتاح لكن ببيانات مختلفة كلياً (مثلاً فاتورة جديدة بمبلغ 1,000,000 ريال بنفس معرف فاتورة سابقة بمبلغ 500 ريال)، فإن المحرك يعيد نتيجة الفاتورة الأولى السابقة بصمت تام ولا يعترض ولا يكتشف التعارض.

---

### 4.2 عيوب ذرية دورة حياة الأصول الثابتة (High - P1)

* **الموقع:** [`internal/subledgers/fixed_assets.go:640-729`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L640-L729)
* **الدليل الميداني:**
  في عمليتي التدني (`Impairment`) والاستبعاد (`Disposal`):

  ```go
  entryID := uuid.New()
  entry := posting.JournalEntry{
      ID: entryID,
      DocumentReference: disposalReference(asset.ID, entryID),
      ...
  }
  ```

  يتم توليد معرف عشوائي جديد `uuid.New()` في كل مرة يتم فيها إنشاء مسودة إهلاك أو استبعاد. لا توجد حالة "عملية معلقة" (Pending Operation Lock) على سجل الأصل.
* **الأثر الحقيقي:**
  يمكن لمستخدمين مختلفين إنشاء مسودتي استبعاد لنفس الأصل في نفس اللحظة. كلا القيدين يمكن ترحيلهما بنجاح إلى دفتر الأستاذ العام (GL)، وعند محاولة تأكيد حالة الأصل، تنجح الأولى وتفشل الثانية، تاركة القيد الثاني مرحلاً بالخطأ في الدفاتر المحاسبية دون وجود أصل مقابل له!

---

### 4.5 فشل شجرة الحسابات الحتمي في PostgreSQL (High - P1)

* **المواقع:**
  * [`internal/masterdata/service.go:93, 106`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/masterdata/service.go#L93)
  * [`storage/postgres/masterdata.go:102-104`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/masterdata.go#L102-L104)
* **الدليل الميداني:**
  في `service.go`:

  ```go
  func (s *Service) UpdateAccountParent(account Account, parentID *uuid.UUID) error {
      existing, err := s.repo.GetAccountByID(account.ID) // السطر 106
  ```

  وفي `storage/postgres/masterdata.go`:

  ```go
  func (d *Driver) GetAccountByID(ctx context.Context, id uuid.UUID) (masterdata.Account, error) {
      return masterdata.Account{}, masterdata.ErrCompanyScopeRequired // السطر 103
  }
  ```

* **الأثر الحقيقي:**
  أي محاولة لتحديث الحساب الأب أو فحص الدورات (`accountWouldCycle`) عند تشغيل النظام على قاعدة بيانات PostgreSQL **ستفشل فوراً وبنسبة 100% بخطأ `ErrCompanyScopeRequired`**. هذا يمنع إدارة شجرة الحسابات تماماً على البيئة الإنتاجية.

---

### 4.6 أتمتة معايير IFRS في الـ CLI تشغل مجدولاً فارغاً (Medium - P2)

* **المواقع:**
  * [`cmd/agy/main.go:78-95`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/cmd/agy/main.go#L78-L95)
  * [`internal/ifrsautomation/service.go:24`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/ifrsautomation/service.go#L24)
* **الدليل الميداني:**
  في `cmd/agy/main.go:78`:

  ```go
  scheduler, err := ifrsautomation.NewScheduler(runner)
  ```

  يتم استدعاء `NewScheduler` دون تمرير أي وظائف `jobs...`. مجدول الأتمتة يعمل كخادم خلفي (Daemon) أو أمر تشغيل دون أن ينفذ أي عملية حسابية فعلية، وفي نفس الوقت يتجاهل الأخطاء العائدة:

  ```go
  _, _ = s.RunPeriod(ctx, period)
  ```

---

### 5.0 واقعية محركات معايير IFRS (IFRS 16, 9, 15, 10)

* **الموقع:** [`internal/ifrs16/lease.go:152, 217`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/ifrs16/lease.go#L152)
* **الدليل الميداني:**

  ```go
  periodicRate := c.AnnualDiscountRate.DivRound(twelve, internal)
  monthlyRate := annualRate.DivRound(twelve, internal)
  ```

  المحرك يفترض تلقائياً وبشكل مفرط التبسيط أن الفترات شهرية دائماً، ومعدل الفائدة يقسم ببساطة على 12، مما يمثل فائدة اسمية بسيطة وليست الفائدة الفعلية المركبة (Effective Interest Rate) المشروطة بمعيار IFRS 16 في حال كانت الدفعات غير متطابقة أو غير منتظمة. وبالمثل، وحدات IFRS 9 و IFRS 15 و IFRS 10 تحتوي على نماذج حسابية تمهيدية ممتازة لكنها لا تغطي متطلبات الاستحواذ المعقدة أو التعديلات العقدية أو قياس تقدم الأداء وفق المدخلات/المخرجات.

---

### 6.2، 6.3، 6.6 عيوب التصميم والمعمارية

1. **واجهة `StorageDriver` (السطر 46-60 في `engine.go`):**  
   تجمع 13 واجهة مختلفة، مما يجبر أي موائم تخزين (مثل Mock أو Driver جديد) على تنفيذ عشرات الدوال، منتهكة مبدأ فصل الواجهات (Interface Segregation Principle).
2. **دالة البناء بـ `any` (السطر 27-44 في `engine.go`):**  
   استخدام `func New(first, second any)` يكسر نظام الفحص الساكن للأنواع في Go من أجل السماح بتبديل ترتيب المعاملات فقط، مما يؤجل اكتشاف أخطاء التكوين إلى وقت التشغيل.
3. **متغير `BaseCurrency` المهجور (السطر 20 في `engine.go`):**  
   يتم تهيئته افتراضياً بـ "USD"، لكن بالبحث في كامل المستودع لا يوجد أي كود مالي يقرأ هذا الحقل أو يعتمد عليه في التحويلات المالية.

---

### 7.1 تجاوز نظام الترحيلات بملف Schema خام (Medium - P2)

* **الموقع:** [`storage/postgres/driver.go:52-66`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L52-L66)
* **الدليل الميداني:**
  دالة `EnsureSchema` تقرأ ملف `schema.sql` من القرص وتنفذه بالكامل على قاعدة البيانات عند كل بدء اتصال قبل تشغيل `MigrateUp()`. هذا يمنع تتبع إصدارات الدوال والـ Triggers ويخلق حالة تعارض دائمة مع سجل الترحيلات `schema_migrations`.

---

## 4. الخلاصة والتوصيات الإلزامية

إن نتائج هذا الفحص تثبت صحة ومصداقية التحليل الوارد في تقرير الفجوات بنسبة تامة.  
نوصي بشدة بالبدء الفوري في خطة الإصلاحات الهندسية وفق الترتيب التالي:

1. **إصلاح Triggers قاعدة البيانات:** تعديل فحص التوازن في `schema.sql` ليعتمد على `functional_debit_minor` و `functional_credit_minor`.
2. **إصلاح التسلسل والترحيل الجماعي:** تعديل `Driver.PostBulk` ليخصص التسلسل داخل نفس المعاملة، وتصحيح `GetNextSequenceWithLock` لتستخدم `FOR UPDATE` وتزيد العداد.
3. **تأمين نظام الحوكمة:** إجبار ربط الـ Approval Gate داخل `NewEngine`، ومطابقة مبالغ طلبات الموافقة مع القيد الحقيقي المخزن، وإضافة قيد `UNIQUE(company_id, entry_id)`.
4. **تكامل سجل التدقيق:** إدراج إضافة سجل التدقيق كخطوة إلزامية داخل نفس معاملة ترحيل القيود.
5. **تصحيح شجرة الحسابات والدفاتر المساعدة:** إضافة دعم سياق الشركة (`CompanyID`) لجميع دوال `masterdata` والتقارير المالية.
