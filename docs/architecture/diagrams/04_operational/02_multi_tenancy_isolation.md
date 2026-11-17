# مخطط عزل المستأجرين وأمان الصفوف (Multi-Tenancy Isolation & RLS)

- **النطاق (Scope):** الآلية الدقيقة لانتقال معرف الشركة (`company_id`) من الطلب إلى دالة `SET LOCAL`، وكيف تُجبر PostgreSQL على حجب بيانات الشركات الأخرى، وآلية تطهير الاتصال لمنع تسرب البيانات.
- **مصدر الحقيقة (Source of Truth):** `internal/adapters/postgres/transactions.go`, `internal/adapters/postgres/migrations/202611100007_force_tenant_rls.sql`, `internal/adapters/postgres/rls_test.go`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: دورة حياة عزل الشركة (Company Isolation Flow)

```mermaid
sequenceDiagram
    autonumber
    participant App as تطبيق المحرك / Application Service
    participant Driver as محول التخزين / Postgres Driver
    participant Pool as حوض الاتصالات / pgxpool.Pool
    participant Conn as اتصال قاعدة البيانات / DB Connection
    participant PG as محرك PostgreSQL / Query Engine & RLS

    App->>Driver: WithCompanyTx(ctx, companyID, fn)
    activate Driver

    Driver->>Pool: طلب اتصال: pool.Acquire(ctx)
    activate Pool
    Pool-->>Driver: تخصيص اتصال فيزيائي (conn)
    deactivate Pool

    Driver->>Conn: بدء معاملة: BEGIN TRANSACTION
    activate Conn

    %% ضبط متغير الجلسة المؤقت
    Driver->>Conn: SELECT set_config('app.current_company_id', $1, true)
    Note over Conn,PG: تم ضبط المتغير مع is_local = true (نطاق المعاملة فقط)

    %% تنفيذ الاستعلامات مقيدة بـ RLS
    Driver->>App: تمرير سياق المعاملة tx
    activate App
    App->>Conn: استعلام: SELECT * FROM account_moves WHERE id = $1
    Conn->>PG: تنفيذ الاستعلام
    activate PG
    Note over PG: محرك RLS يطبق تلقائيًا:<br/>WHERE company_id = app_current_company_id()<br/><b>يستحيل قراءة أي صف لشركة أخرى</b>
    PG-->>Conn: إرجاع صفوف الشركة المحددة فقط
    deactivate PG
    App-->>Driver: انتهاء تنفيذ الأعمال
    deactivate App

    %% إنهاء المعاملة وتطهير الجلسة
    Driver->>Conn: تثبيت المعاملة: COMMIT (أو ROLLBACK)
    Note over Conn,PG: عند انتهاء المعاملة، يُصفّر PostgreSQL تلقائيًا<br/>متغير app.current_company_id إلى NULL
    deactivate Conn

    %% ضمان التطهير الإضافي قبل إعادة الاتصال للحوض
    Driver->>Conn: تحقق إضافي اختياري: DISCARD TEMP / RESET ALL
    Driver->>Pool: إعادة الاتصال نظيفًا للحوض: conn.Release()
    
    Driver-->>App: انتهاء العملية بنجاح
    deactivate Driver
```

---

## 2. سياسة الأمان المفروضة إجباريًا (FORCE ROW LEVEL SECURITY)

لضمان عدم تجاوز سياسة العزل حتى من قِبل مالك الجدول (Table Owner):

```sql
-- تطبيق العزل الإجباري على جدول القيود
ALTER TABLE account_moves ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_moves FORCE ROW LEVEL SECURITY;

-- السياسة المطبقة على كافة عمليات القراءة والكتابة
CREATE POLICY tenant_isolation_policy ON account_moves
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

-- الدالة المساعدة لقراءة معرف الشركة
CREATE OR REPLACE FUNCTION app_current_company_id() RETURNS uuid AS $$
    SELECT NULLIF(current_setting('app.current_company_id', true), '')::uuid
$$ LANGUAGE sql STABLE;
```

---

## 3. نتائج اختبارات الاختراق والعزل (RLS Penetration Tests)

تُثبت حزمة الاختبارات في `internal/adapters/postgres/phase6_rls_penetration_test.go` الحالات التالية:

1. **محاولة استعلام بلا هوية (`No Tenant Context`):** ترجع `0` صفوف؛ لا يُعرض أي بيان لأي شركة.
2. **محاولة حقن شركة ضحية (`Cross-Tenant Injection`):** محاولة تمرير `company_id` مختلف في استعلام `INSERT` يفشل فورًا بخرق سياسة `WITH CHECK`.
3. **تطهير الاتصال (Connection Hygiene):** إعادة استخدام الاتصال بعد تراجع أو فشل المعاملة يضمن أن القيمة العالقة هي `NULL`، مما يحمي حوض الاتصالات المشترك.
