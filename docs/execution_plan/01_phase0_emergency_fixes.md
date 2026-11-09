# المرحلة 0: إصلاحات الطوارئ — سلامة المحاسبة ونزاهة البيانات

## Phase 0: Emergency Fixes — Accounting Correctness & Data Integrity (P0)

---

> [!CAUTION]
> **الأولوية:** قصوى وعاجلة (Critical / P0)
> **المدة:** أسبوعان (Week 1-2)
> **الهدف:** إصلاح جميع المشكلات الحرجة التي تُنتج بيانات مالية خاطئة أو تسمح بالتلاعب بالقيود المرحّلة.
> **الشرط:** لا يُسمح بأي عمل تطويري آخر حتى اكتمال هذه المرحلة.

---

## فهرس المهام

| المهمة | الوصف | المشكلة المرجعية | المعيار الملزم |
| :---: | :--- | :---: | :--- |
| **T0.1** | إصلاح الحسابات العكسية في قائمة الدخل | #1 | IAS 1, IFRS 15, ISO 25010 |
| **T0.2** | سد ثغرات تعديل القيود بعد الترحيل في PostgreSQL | #5 | SOX 404, SOC 1 (CC6.1), ISO 27001 |
| **T0.3** | ربط تحديث الأصول باكتمال ترحيل القيد ذرياً | #9 | IAS 16, IFRS Framework, ISA 500 |
| **T0.4** | فرض توازن القيد المحاسبي في قاعدة البيانات | #6 | ISO/IEC 9075 (ACID), SOC 2 |

---

## T0.1 — إصلاح الحسابات العكسية في قائمة الدخل

### Fix Contra Account Handling in Income Statement

**المشكلة المرجعية:** المشكلة #1 — الحسابات العكسية لا تُعالج في قائمة الدخل
**مستوى الخطورة:** 🔴 حرجة (Critical)
**الأثر المالي:** تضخيم الإيرادات وتشويه صافي الدخل — أخطر أنواع التحريف المالي

### 1. الملفات المتأثرة

| الملف | نوع التعديل | السبب |
| :--- | :---: | :--- |
| [`internal/reporting/income_statement.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/income_statement.go) | **تعديل** | إضافة فحص `IsContraAccount` |
| [`internal/reporting/reporting.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/reporting.go) | **تعديل** | توحيد دالة احتساب الرصيد المحاسبي |
| [`internal/reporting/balance_sheet.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/balance_sheet.go) | **مرجع** | النمط الصحيح الموجود حالياً |
| `internal/reporting/income_statement_test.go` | **إنشاء/تعديل** | اختبارات الحسابات العكسية |

### 2. التعديل البرمجي المطلوب

#### الخطوة أ: توحيد دالة احتساب الرصيد المحاسبي

إنشاء دالة موحدة في [`reporting.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/reporting.go) تُستخدم في جميع التقارير:

```go
// adjustedAccountBalance returns the account balance with contra account
// sign adjustment applied. This ensures consistent handling across all
// financial statements (IAS 1, paragraphs 32-35).
func adjustedAccountBalance(balance decimal.Decimal, account masterdata.Account) decimal.Decimal {
    amount := accountBalance(balance, account)
    if account.IsContraAccount {
        amount = amount.Neg()
    }
    return amount
}
```

#### الخطوة ب: تعديل income_statement.go

في [`income_statement.go#L56-L81`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/income_statement.go#L56-L81)، استبدال:

```go
// قبل (خاطئ):
amount := accountBalance(balances[account.ID], account)

// بعد (صحيح):
amount := adjustedAccountBalance(balances[account.ID], account)
```

#### الخطوة ج: تحديث balance_sheet.go

استبدال الكود المنفصل الحالي بالدالة الموحدة:

```go
// قبل:
amount := accountBalance(balances[account.ID], account)
if account.IsContraAccount {
    amount = amount.Neg()
}

// بعد:
amount := adjustedAccountBalance(balances[account.ID], account)
```

### 3. اختبارات القبول (Acceptance Tests)

```go
// Test_IncomeStatement_ContraAccount_SalesReturns
// السيناريو: تسجيل مردودات مبيعات 100 ر.س كحساب إيراد عكسي
// المتوقع: صافي الإيرادات = الإيراد الإجمالي - المردودات
func Test_IncomeStatement_ContraAccount_SalesReturns(t *testing.T) {
    // 1. إنشاء حساب إيراد (Sales Revenue) بمبلغ 1000
    // 2. إنشاء حساب إيراد عكسي (Sales Returns) IsContraAccount=true بمبلغ 100
    // 3. توليد قائمة الدخل
    // 4. التأكد أن Revenue = 900 (وليس 1100)
}

// Test_IncomeStatement_ContraAccount_COGS_Discount
// السيناريو: خصم على المشتريات كحساب مصروف عكسي
func Test_IncomeStatement_ContraAccount_COGS_Discount(t *testing.T) {
    // 1. إنشاء حساب مصروف (COGS) بمبلغ 500
    // 2. إنشاء حساب مصروف عكسي (Purchase Discount) IsContraAccount=true بمبلغ 50
    // 3. التأكد أن COGS الصافي = 450
}

// Test_AllReports_ContraAccount_Consistency
// السيناريو: التأكد من تطابق معالجة الحسابات العكسية عبر جميع التقارير
func Test_AllReports_ContraAccount_Consistency(t *testing.T) {
    // التأكد أن balance_sheet و income_statement يستخدمان نفس الدالة
}
```

### 4. معايير القبول النهائية

- [ ] الحسابات العكسية في قائمة الدخل تُخصم من فئتها الأصلية (الإيرادات أو المصروفات)
- [ ] جميع التقارير المالية تستخدم دالة `adjustedAccountBalance` الموحدة
- [ ] اجتياز اختبارات الحسابات العكسية لجميع أنواع الحسابات (إيرادات، مصروفات، أصول، التزامات)
- [ ] عدم وجود أي استدعاء مباشر لـ `accountBalance` في التقارير دون فحص ContraAccount

---

## T0.2 — سد ثغرات تعديل القيود بعد الترحيل

### Fix Ledger Immutability Vulnerabilities in PostgreSQL

**المشكلة المرجعية:** المشكلة #5 — ثغرة JSONB Mutation و Line Relinking
**مستوى الخطورة:** 🔴 حرجة (Critical)
**الأثر الأمني:** خرق نزاهة دفتر الأستاذ العام — يتيح التلاعب بالقيود المرحّلة

### 1. الملفات المتأثرة

| الملف | نوع التعديل | السبب |
| :--- | :---: | :--- |
| [`storage/postgres/schema.sql`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql) | **تعديل** | إضافة مشغلات حماية جديدة |
| `storage/postgres/migrations/` | **إنشاء** | ملف ترحيل لتطبيق التعديلات |
| `storage/postgres/immutability_test.go` | **إنشاء** | اختبارات اختراق الثغرات |

### 2. التعديلات المطلوبة

#### الثغرة الأولى: غياب حماية جدول `account_moves` بعد الترحيل

```sql
-- Trigger 1: منع أي تعديل على رأس القيد بعد الترحيل
-- يحمي حقل entry JSONB وجميع الحقول الأخرى
CREATE OR REPLACE FUNCTION prevent_posted_move_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'posted' THEN
        -- السماح فقط بتغيير الحالة إلى reversed (عبر قيد عكسي)
        IF NEW.status = 'reversed' AND
           OLD.entry = NEW.entry AND
           OLD.journal_id = NEW.journal_id AND
           OLD.company_id = NEW.company_id THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION
            'Cannot modify posted move (id=%). Use reversal entry instead.',
            OLD.id
        USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_posted_move_mutation
    BEFORE UPDATE ON account_moves
    FOR EACH ROW
    EXECUTE FUNCTION prevent_posted_move_mutation();

-- منع حذف القيود المرحّلة نهائياً
CREATE TRIGGER trg_prevent_posted_move_delete
    BEFORE DELETE ON account_moves
    FOR EACH ROW
    WHEN (OLD.status = 'posted')
    EXECUTE FUNCTION prevent_posted_move_mutation();
```

#### الثغرة الثانية: فحص `NEW.move_id` بدلاً من `OLD.move_id`

```sql
-- تعديل Trigger الحالي لفحص OLD.move_id عند UPDATE
CREATE OR REPLACE FUNCTION protect_posted_move_lines()
RETURNS TRIGGER AS $$
DECLARE
    old_move_status TEXT;
    new_move_status TEXT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        SELECT status INTO old_move_status
        FROM account_moves WHERE id = OLD.move_id;
        IF old_move_status = 'posted' THEN
            RAISE EXCEPTION
                'Cannot delete line from posted move (move_id=%)',
                OLD.move_id;
        END IF;
        RETURN OLD;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        -- فحص القيد القديم (OLD) — الإصلاح الحرج
        SELECT status INTO old_move_status
        FROM account_moves WHERE id = OLD.move_id;
        IF old_move_status = 'posted' THEN
            RAISE EXCEPTION
                'Cannot modify line belonging to posted move (move_id=%)',
                OLD.move_id;
        END IF;

        -- فحص القيد الجديد (NEW) أيضاً لمنع الربط بقيد مرحل
        IF OLD.move_id != NEW.move_id THEN
            SELECT status INTO new_move_status
            FROM account_moves WHERE id = NEW.move_id;
            IF new_move_status = 'posted' THEN
                RAISE EXCEPTION
                    'Cannot attach line to posted move (move_id=%)',
                    NEW.move_id;
            END IF;
        END IF;
        RETURN NEW;
    END IF;

    -- INSERT: فحص أن القيد الهدف ليس مرحلاً
    SELECT status INTO new_move_status
    FROM account_moves WHERE id = NEW.move_id;
    IF new_move_status = 'posted' THEN
        RAISE EXCEPTION
            'Cannot insert line into posted move (move_id=%)',
            NEW.move_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

### 3. اختبارات القبول — اختبارات اختراق (Penetration Tests)

```sql
-- اختبار 1: محاولة تعديل حقل entry JSONB في قيد مرحل → يجب أن يفشل
UPDATE account_moves
SET entry = '{"tampered": true}'::jsonb
WHERE id = '<posted_move_id>';
-- Expected: ERROR restrict_violation

-- اختبار 2: محاولة نقل سطر من قيد مرحل إلى قيد مسودة → يجب أن يفشل
UPDATE account_move_lines
SET move_id = '<draft_move_id>'
WHERE move_id = '<posted_move_id>';
-- Expected: ERROR (Cannot modify line belonging to posted move)

-- اختبار 3: محاولة حذف قيد مرحل → يجب أن يفشل
DELETE FROM account_moves WHERE id = '<posted_move_id>';
-- Expected: ERROR

-- اختبار 4: محاولة حذف سطر من قيد مرحل → يجب أن يفشل
DELETE FROM account_move_lines WHERE move_id = '<posted_move_id>';
-- Expected: ERROR

-- اختبار 5: السماح بتعديل قيد مسودة → يجب أن ينجح
UPDATE account_moves
SET entry = '{"updated": true}'::jsonb
WHERE id = '<draft_move_id>';
-- Expected: SUCCESS
```

### 4. معايير القبول النهائية

- [ ] لا يمكن تعديل أي حقل في `account_moves` إذا كانت `status = 'posted'` (ما عدا التحويل إلى `reversed`)
- [ ] لا يمكن نقل سطر من قيد مرحل إلى أي قيد آخر
- [ ] لا يمكن حذف أسطر أو رؤوس القيود المرحّلة
- [ ] القيود المسودة لا تزال قابلة للتعديل بشكل طبيعي
- [ ] اجتياز جميع اختبارات الاختراق الخمسة أعلاه

---

## T0.3 — ربط تحديث الأصول باكتمال ترحيل القيد

### Defer Asset Ledger Mutation Until Posting Confirmation

**المشكلة المرجعية:** المشكلة #9 — تحديث حالة الأصل يسبق ترحيل القيد المحاسبي
**مستوى الخطورة:** 🔴 حرجة (Critical)
**الأثر المالي:** انفصال كارثي بين الدفتر المساعد وحساب الرقابة في الأستاذ العام

### 1. الملفات المتأثرة

| الملف | نوع التعديل | السبب |
| :--- | :---: | :--- |
| [`internal/subledgers/fixed_assets.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go) | **تعديل** | فصل إنشاء المسودة عن تحديث الأصل |
| `internal/subledgers/fixed_assets_test.go` | **تعديل** | اختبارات السيناريو الجديد |

### 2. النمط المعماري المطلوب (Target Pattern)

```mermaid
flowchart TD
    A["1. حساب الإهلاك الشهري"] --> B["2. إنشاء مسودة القيد Draft"]
    B --> C["3. إرجاع المسودة للمستدعي"]
    C --> D{"4. هل تم ترحيل القيد؟"}
    D -->|نعم Posted| E["5. تحديث سجل الأصل ذرياً"]
    D -->|لا Rejected| F["6. لا يتغير شيء في سجل الأصل"]
    E --> G["7. اكتمال العملية بنجاح"]

    style A fill:#e3f2fd
    style B fill:#fff3e0
    style D fill:#fce4ec
    style E fill:#e8f5e9
    style F fill:#ffebee
```

### 3. التعديل البرمجي المطلوب

#### الاستراتيجية: فصل المسؤوليات (Separation of Concerns)

```go
// الطريقة الحالية (خاطئة): تحفظ الأصل المعدل مع المسودة معاً
func (l *FixedAssetLedger) MonthlyDepreciation(...) (posting.JournalEntry, Money, error) {
    // ...
    updatedAsset.CurrentBookValue = bookValue  // تحديث مبكر!
    l.saveAssetAndDraft(asset, updatedAsset, entry)  // حفظ فوري!
}

// الطريقة المطلوبة: إنشاء المسودة فقط، وتأجيل تحديث الأصل
func (l *FixedAssetLedger) MonthlyDepreciation(...) (posting.JournalEntry, Money, error) {
    // 1. حساب مبلغ الإهلاك
    depAmount := calculateDepreciation(asset, asOf)

    // 2. إنشاء مسودة القيد فقط (بدون تعديل الأصل)
    entry := posting.JournalEntry{
        Status: posting.EntryDraft,
        // ...
    }
    if err := l.saveDraftOnly(entry); err != nil {
        return posting.JournalEntry{}, Money{}, err
    }
    return entry, depAmount, nil
}

// دالة جديدة: تُستدعى فقط بعد نجاح الترحيل
func (l *FixedAssetLedger) ConfirmDepreciation(
    assetID uuid.UUID,
    postedEntryID uuid.UUID,
    depAmount Money,
    asOf time.Time,
) error {
    // 1. التحقق أن القيد فعلاً مرحل (posted)
    // 2. تحديث AccumulatedDepreciation و CurrentBookValue
    // 3. تحديث LastDepreciationDate
    // كل ذلك داخل معاملة ذرية واحدة
}
```

#### تطبيق نفس النمط على ImpairAsset و DisposeAsset

نفس المشكلة موجودة في دوال [`ImpairAsset`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L142-L158) و `DisposeAsset`:

```go
// كلتا الدالتين تُنشئان Entry بحالة Draft وتحدثان الأصل فوراً
// يجب تطبيق نفس نمط الفصل: Draft أولاً → Posting → Confirm
```

### 4. اختبارات القبول

```go
// Test_MonthlyDepreciation_DraftOnly_NoAssetChange
// السيناريو: تشغيل الإهلاك الشهري دون ترحيل القيد
// المتوقع: سجل الأصل لم يتغير
func Test_MonthlyDepreciation_DraftOnly_NoAssetChange(t *testing.T) {
    asset := createTestAsset(cost: 10000, accumulated: 0)
    entry, _, _ := ledger.MonthlyDepreciation(asset, asOf)
    assert.Equal(t, posting.EntryDraft, entry.Status)

    // التأكد أن الأصل لم يتغير
    reloaded := loadAsset(asset.ID)
    assert.Equal(t, decimal.NewFromInt(10000), reloaded.CurrentBookValue)
    assert.Equal(t, decimal.Zero, reloaded.AccumulatedDepreciation)
}

// Test_MonthlyDepreciation_AfterPosting_AssetUpdated
// السيناريو: ترحيل القيد ثم تأكيد الإهلاك
// المتوقع: سجل الأصل يعكس الإهلاك
func Test_MonthlyDepreciation_AfterPosting_AssetUpdated(t *testing.T) {
    entry, depAmount, _ := ledger.MonthlyDepreciation(asset, asOf)
    postedEntry := postEngine.Post(entry)
    ledger.ConfirmDepreciation(asset.ID, postedEntry.ID, depAmount, asOf)

    reloaded := loadAsset(asset.ID)
    assert.Equal(t, depAmount, reloaded.AccumulatedDepreciation)
}

// Test_MonthlyDepreciation_RejectedDraft_CanRerun
// السيناريو: المسودة رُفضت → إعادة تشغيل الإهلاك لنفس الشهر
// المتوقع: يُنتج مسودة جديدة بنجاح (لا يقول "تم بالفعل")
func Test_MonthlyDepreciation_RejectedDraft_CanRerun(t *testing.T) {
    entry1, _, _ := ledger.MonthlyDepreciation(asset, asOf)
    rejectDraft(entry1.ID)

    // يجب أن ينجح لأن الأصل لم يتحدث
    entry2, _, err := ledger.MonthlyDepreciation(asset, asOf)
    assert.NoError(t, err)
    assert.NotEqual(t, entry1.ID, entry2.ID)
}
```

### 5. معايير القبول النهائية

- [ ] `MonthlyDepreciation` لا تُعدّل سجل الأصل — تُنشئ مسودة فقط
- [ ] `ImpairAsset` لا تُعدّل سجل الأصل — تُنشئ مسودة فقط
- [ ] `DisposeAsset` لا تُعدّل سجل الأصل — تُنشئ مسودة فقط
- [ ] دالة `ConfirmDepreciation` تُحدّث الأصل فقط بعد التحقق من ترحيل القيد
- [ ] إعادة تشغيل الإهلاك لشهر تم رفض مسودته يعمل بنجاح
- [ ] لا يوجد أي مسار يُحدّث الأصل قبل اكتمال الترحيل

---

## T0.4 — فرض توازن القيد المحاسبي في قاعدة البيانات

### Add Deferred Balance Constraint in PostgreSQL

**المشكلة المرجعية:** المشكلة #6 — توازن القيد غير مفروض في قاعدة البيانات
**مستوى الخطورة:** 🟠 عالية (High)
**الأثر:** أي كتابة مباشرة في DB أو خطأ برمجي يمكن أن يُفسد دفتر الأستاذ العام

### 1. الملفات المتأثرة

| الملف | نوع التعديل | السبب |
| :--- | :---: | :--- |
| [`storage/postgres/schema.sql`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql) | **تعديل** | إضافة Constraint Trigger مؤجل |
| `storage/postgres/migrations/` | **إنشاء** | ملف ترحيل |

### 2. التعديل المطلوب

```sql
-- قيد فحص مؤجل: يتحقق من توازن القيد عند COMMIT
-- يسمح بإدخال الأسطر واحداً تلو الآخر خلال المعاملة
-- لكن يرفض COMMIT إذا كان المجموع غير متوازن

CREATE OR REPLACE FUNCTION check_move_balanced()
RETURNS TRIGGER AS $$
DECLARE
    total_debit  NUMERIC;
    total_credit NUMERIC;
    move_status  TEXT;
BEGIN
    -- فقط نفحص القيود المرحّلة (المسودات قد تكون غير متوازنة مؤقتاً)
    SELECT status INTO move_status
    FROM account_moves
    WHERE id = COALESCE(NEW.move_id, OLD.move_id);

    IF move_status = 'posted' THEN
        SELECT COALESCE(SUM(debit), 0),
               COALESCE(SUM(credit), 0)
        INTO total_debit, total_credit
        FROM account_move_lines
        WHERE move_id = COALESCE(NEW.move_id, OLD.move_id);

        IF total_debit != total_credit THEN
            RAISE EXCEPTION
                'Move % is unbalanced: total_debit=% != total_credit=%',
                COALESCE(NEW.move_id, OLD.move_id),
                total_debit,
                total_credit
            USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

-- DEFERRABLE INITIALLY DEFERRED: يُفحص عند COMMIT وليس عند كل INSERT
CREATE CONSTRAINT TRIGGER trg_check_move_balanced
    AFTER INSERT OR UPDATE OR DELETE ON account_move_lines
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION check_move_balanced();
```

### 3. اختبارات القبول

```sql
-- اختبار 1: إدخال أسطر متوازنة → COMMIT ينجح
BEGIN;
INSERT INTO account_move_lines (move_id, debit, credit) VALUES ('...', 1000, 0);
INSERT INTO account_move_lines (move_id, debit, credit) VALUES ('...', 0, 1000);
UPDATE account_moves SET status = 'posted' WHERE id = '...';
COMMIT;  -- Expected: SUCCESS

-- اختبار 2: إدخال أسطر غير متوازنة → COMMIT يفشل
BEGIN;
INSERT INTO account_move_lines (move_id, debit, credit) VALUES ('...', 1000, 0);
INSERT INTO account_move_lines (move_id, debit, credit) VALUES ('...', 0, 500);
UPDATE account_moves SET status = 'posted' WHERE id = '...';
COMMIT;  -- Expected: ERROR check_violation
```

### 4. معايير القبول النهائية

- [ ] أي قيد مرحل يجب أن يكون متوازناً (∑Debit = ∑Credit) وإلا يُرفض عند COMMIT
- [ ] القيود المسودة لا تخضع لهذا الفحص (مرونة أثناء التحرير)
- [ ] لا يتأثر أداء الترحيل بشكل ملحوظ (< 5% overhead)
- [ ] اجتياز اختبارات الترحيل الحالية بنجاح تام

---

## ملخص المرحلة 0 — معايير الانتقال للمرحلة التالية

> [!IMPORTANT]
> **شروط الانتقال إلى المرحلة 1:**

- [ ] ✅ T0.1: الحسابات العكسية تعمل بشكل صحيح في جميع التقارير
- [ ] ✅ T0.2: لا يمكن تعديل أو حذف أي قيد/سطر مرحل من قاعدة البيانات
- [ ] ✅ T0.3: لا يتم تحديث الأصول إلا بعد تأكيد ترحيل القيد
- [ ] ✅ T0.4: قاعدة البيانات ترفض القيود غير المتوازنة عند COMMIT
- [ ] ✅ جميع الاختبارات الحالية + الجديدة تمر بنجاح (`go test ./...`)
- [ ] ✅ لا يوجد أي Regression في الوظائف القائمة

### تأثير المرحلة على نسب الامتثال

| المعيار | قبل المرحلة 0 | بعد المرحلة 0 | التحسن |
| :--- | :---: | :---: | :---: |
| **IFRS / IAS** | 40% | 55% | +15% |
| **SOX 404** | 30% | 50% | +20% |
| **SOC 1/2** | 35% | 55% | +20% |
| **ISO 25010** | 55% | 70% | +15% |

