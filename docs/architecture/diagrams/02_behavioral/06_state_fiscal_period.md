# آلة حالة الفترة المالية وإعادة الفتح (State Machine: Fiscal Period)

- **النطاق (Scope):** التحولات المسموحة لحالة الفترة المالية (`open` → `soft_closed` → `hard_closed`) ومسار إعادة الفتح الاستثنائي الخاضع للتدقيق والموافقة (`Audited Reopening`).
- **مصدر الحقيقة (Source of Truth):** `internal/masterdata/fiscal_calendar.go`, `internal/adapters/postgres/migrations/202611170001_p9_period_reopen_approval.sql`, `docs/accounting/closing_and_reopening_policy.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: دورة حياة الفترة المالية

```mermaid
stateDiagram-v2
    [*] --> Open : إنشاء السنة المالية وتقسيم الفترات

    state Open {
        [*] --> ActivePosting
        ActivePosting : مسموح: ترحيل كافة العمليات التشغيلية العادية
        ActivePosting : مسموح: إنشاء وتعديل المسودات
        ActivePosting : مسموح: استلام أحداث المبيعات والمشتريات
    }

    Open --> SoftClosed : الإقفال المؤقت / الشهري (ClosePeriod)<br/>[المالك: المحاسب المالي / Finance Controller]

    state SoftClosed {
        [*] --> ReviewMode
        ReviewMode : مسموح: قيود التسوية الختامية المعتمدة فقط
        ReviewMode : ممنوع: العمليات التشغيلية العادية للشركاء
        ReviewMode : تنبيه: يتطلب صلاحية تجاوز خاصة (Exception Authorization)
    }

    SoftClosed --> HardClosed : الإقفال النهائي الدائم (HardClosePeriod)<br/>[المالك: المدير المالي التنفيذي / CFO]

    state HardClosed {
        [*] --> ImmutableFreeze
        ImmutableFreeze : <b>ممنوع تماماً: ترحيل أي قيد جديد بتاريخ الفترة</b>
        ImmutableFreeze : <b>ممنوع تماماً: تعديل أي أرصدة تخص هذه الفترة</b>
        ImmutableFreeze : البيانات مجمدة لأغراض المراجعة الخارجية والضريبية
    }

    %% مسار إعادة الفتح الاستثنائي
    HardClosed --> Open : إعادة فتح استثنائية مدققة (ReopenPeriodContext)<br/>[شروط صارمة: ActorID + Reason نصي صريح + ApprovalID]
    SoftClosed --> Open : إعادة فتح للمراجعة (ReopenPeriodContext)<br/>[شروط: ActorID + Reason نصي]

    HardClosed --> [*] : أرشفة السنة المالية المكتملة
```

---

## 2. مصفوفة التحولات والجهات المالكة والصلاحيات

| الحالة الابتدائية | الحالة المستهدفة | الدالة المسؤولة | الجهة المالكة (Owner) | الشروط الإلزامية في الكود |
| --- | --- | --- | --- | --- |
| `open` | `soft_closed` | `ValidatePeriodTransition` | مدير الحسابات / Controller | استكمال قيود الشهر ومطابقة الذمم المبدئية. |
| `soft_closed` | `hard_closed` | `ValidatePeriodTransition` | المدير المالي / CFO | نجاح قائمة التحقق الآلية (`Pre-Close Checklist`). |
| `open` | `hard_closed` | `ValidatePeriodTransition` | **مرفوض** | لا يجوز القفز المباشر؛ يلزم المرور بـ `soft_closed`. |
| `soft_closed` | `open` | `ValidatePeriodReopen` | المشرف المالي المعتمد | توفر معرف الفاعل (`actorID != Nil`) وسبب نصي مسبب. |
| `hard_closed` | `open` | `ValidatePeriodReopenWithApproval` | مجلس الإدارة / المدقق الخارجي | **إلزامي:** `actorID` + `reason` + **طلب موافقة معتمد (`approvalID`)** وفق هجرة `202611170001`. |

---

## 3. الضمانة على مستوى قاعدة البيانات

> [!IMPORTANT] **الحماية في النواة والمحول**
> عند وصول الفترة إلى `hard_closed`، ترفض دالة `d.updateAccountBalancesLocked` في محول PostgreSQL وأيضًا `posting.prepareForPosting` أي محاولة لترحيل قيود جديدة بتاريخ يقع ضمن تلك الفترة، مع إرجاع الخطأ المحاسبي `masterdata.ErrFiscalPeriodClosed`.
