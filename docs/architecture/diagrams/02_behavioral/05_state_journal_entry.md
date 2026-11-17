# آلة حالة القيد المحاسبي (State Machine: JournalEntry Lifecycle)

- **النطاق (Scope):** الحالات المسموحة للقيد المحاسبي (`Draft` → `Posted` → `Reversed`) وما هو مسموح وممنوع قطعيًا في كل حالة.
- **مصدر الحقيقة (Source of Truth):** `internal/posting/entry.go`, `internal/posting/engine.go`, `internal/posting/reversal.go`, `internal/adapters/postgres/migrations/202611010001_baseline_schema.sql`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: دورة حياة القيد

```mermaid
stateDiagram-v2
    [*] --> Draft : إنشاء قيد جديد (CreateDraft / SaveDraft)

    state Draft {
        [*] --> InEdit
        InEdit : مسموح: تعديل السطور والمبالغ
        InEdit : مسموح: تعديل مركز التكلفة والشركاء
        InEdit : مسموح: الحذف الفيزيائي التام (Delete)
        InEdit : ممنوع: التأثير على ميزان المراجعة أو الأرصدة
        InEdit : ممنوع: منح رقم متسلسل رسمي للقيد
    }

    Draft --> Posted : ترحيل القيد (PostEntry)<br/>[شرط: متوازن + فترة مفتوحة + حسابات نشطة + اعتماد]
    Draft --> [*] : حذف المسودة نهائياً

    state Posted {
        [*] --> ImmutableLedger
        ImmutableLedger : مسموح: القراءة وتوليد التقارير وميزان المراجعة
        ImmutableLedger : مسموح: الربط مع المستندات ومطابقة الذمم
        ImmutableLedger : مسموح: طلب العكس المحاسبي (Reverse)
        ImmutableLedger : <b>ممنوع منعاً باتاً: تعديل أي حقل أو سطر (Trigger Guard)</b>
        ImmutableLedger : <b>ممنوع منعاً باتاً: الحذف من قاعدة البيانات (No DELETE)</b>
        ImmutableLedger : <b>ممنوع منعاً باتاً: تعديل رقم اليومية المتسلسل</b>
    }

    Posted --> Reversed : عكس القيد (ReverseEntry)<br/>[توليد قيد عكسي جديد + ربط reversed_move_id]

    state Reversed {
        [*] --> HistoricClosed
        HistoricClosed : مسموح: التدقيق التاريخي وتتبع سبب الإلغاء
        HistoricClosed : مسموح: فحص أثر القيد العكسي المقترن
        HistoricClosed : <b>ممنوع: إعادة الترحيل أو التعديل</b>
        HistoricClosed : <b>ممنوع: الحذف الفيزيائي أو التفريغ (No TRUNCATE)</b>
        HistoricClosed : <b>ممنوع: إعادة العكس مرة أخرى (No Double Reversal)</b>
    }

    Reversed --> [*] : حالة نهائية دائمة للأبد (Permanent Record)
```

---

## 2. مصفوفة الصلاحيات والحظر لكل حالة

| العملية / الإجراء | حالة مسودة (`draft`) | حالة مرحل (`posted`) | حالة معكوس (`reversed`) |
| --- | --- | --- | --- |
| **تعديل السطور والمبالغ** | **مسموح** | **ممنوع قطعيًا** (خطأ: `ErrCannotModifyPostedMove` + زناد DB) | **ممنوع قطعيًا** |
| **حذف السجل (DELETE SQL)** | **مسموح** (للمسودات فقط) | **مرفوض بـ Exception** من خلال زناد `trg_prevent_posted_move_mutation` | **مرفوض بـ Exception** |
| **التأثير على الأرصدة (`account_balances`)** | لا يوجد أي أثر مالي | يُضاف للأرصدة عبر `trg_update_balances_on_posting` | يُلغى الأثر المحاسبي بالقيد العكسي المناظر |
| **تخصيص الرقم المتسلسل (`SequenceNumber`)** | لا يوجد (قيمته 0) | إلزامي ودائم ويمنع تعديله نهائيًا | يظل محتفظًا برقمه الأصلي |
| **الظهور في القوائم المالية الرسمية** | غير مرئي في التقارير المعتمدة | يظهر ويدخل في حساب ميزان المراجعة والأرباح | يظهر مع القيد العكسي ليتلاشى أثرهما الصافي |
| **عملية العكس المحاسبي (`Reverse`)** | غير قابلة للعكس (تُحذف فقط) | **مسموح** إذا لم تكن معكوسة مسبقًا | **ممنوع** (خطأ: `ErrEntryAlreadyReversed`) |
