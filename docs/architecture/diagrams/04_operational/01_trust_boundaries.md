# مخطط حدود الثقة والمصادقة (Trust Boundaries & Service Authentication)

- **النطاق (Scope):** حدود الثقة والتحقق عند تشغيل المحرك كخدمة مستقلة (`cmd/accounting-service`)، وانتقال الهوية من ترويسات الشبكة إلى كائن `Principal` وصولًا إلى حوض الاتصالات.
- **مصدر الحقيقة (Source of Truth):** `internal/serviceauth`, `docs/architecture/service_boundary.md`, `ADR-0004`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: مستويات وحدود الثقة (Trust Boundaries)

```mermaid
flowchart TB
    classDef untrusted fill:#fee2e2,stroke:#dc2626,stroke-width:2px,color:#7f1d1d;
    classDef boundary fill:#fef3c7,stroke:#d97706,stroke-width:2px,color:#0f172a;
    classDef trustedApp fill:#e0f2fe,stroke:#0284c7,stroke-width:2px,color:#0f172a;
    classDef secureDB fill:#ecfdf5,stroke:#059669,stroke-width:3px,color:#0f172a;

    subgraph ZoneUntrusted ["المنطقة غير الموثوقة (Untrusted Network Zone)"]
        ClientRequest["طلب عميل خارجي (HTTP / gRPC)<br/>يحمل JWT أو توقيع Gateway<br/><b>لا ثقة في actor_id أو company_id بالـ JSON body</b>"]:::untrusted
    end

    subgraph ZoneEdge ["حد الثقة الشبكي (Edge Transport Boundary)"]
        AuthInterceptor["متحقق الهوية والمصادقة (internal/serviceauth)<br/>1. مطابقة التوقيع: HMAC-SHA256 أو RS256 JWT<br/>2. فحص صلاحية الوقت والـ Clock Skew (<= 60s)<br/>3. استخراج Identity: {ActorID, TenantID, CompanyIDs}"]:::boundary
    end

    subgraph ZoneApplication ["المنطقة الموثوقة للمحرك (Trusted Application Context)"]
        PrincipalContext["حقن الهوية وسياق التنفيذ<br/><code>publicapplication.WithPrincipal(ctx, principal)</code><br/>فحص: هل الشركة المستهدفة ضمن Principal.CompanyIDs؟"]:::trustedApp
        UseCases["حالات الاستخدام ومحرك الترحيل<br/>(Core Posting Engine / Subledgers / Reports)"]:::trustedApp
    end

    subgraph ZoneDataIsolation ["منطقة أمان وعزل البيانات (PostgreSQL Security Zone)"]
        TxBoundary["بدء معاملة بقاعدة البيانات<br/><code>tx, err := pool.Begin(ctx)</code>"]:::secureDB
        SetLocal["ضبط عزل المستأجر الصارم على مستوى المعاملة<br/><code>SELECT set_config('app.current_company_id', $1, true)</code><br/>(is_local = true / SET LOCAL)"]:::secureDB
        RLSPolicies["سياسات أمان مستوى الصفوف (Row Level Security)<br/><code>USING (company_id = app_current_company_id())</code><br/><code>FORCE ROW LEVEL SECURITY</code>"]:::secureDB
        PoolRelease["تصفير المتغير وإعادة الاتصال لحوض pgxpool<br/>(Prevent Connection Pool Poisoning)"]:::secureDB
    end

    ClientRequest -->|التحقق عند الحد| AuthInterceptor
    AuthInterceptor -->|تحويل لهوية موثوقة| PrincipalContext
    PrincipalContext -->|استدعاء حالات الاستخدام| UseCases
    UseCases -->|فتح المعاملة| TxBoundary
    TxBoundary -->|ضبط سياق الشركة| SetLocal
    SetLocal -->|تنفيذ الاستعلامات مقيدة بـ RLS| RLSPolicies
    RLSPolicies -->|إغلاق المعاملة وتطهير الجلسة| PoolRelease
```

---

## 2. جدول المدخلات الموثوقة وغير الموثوقة

| المدخل | موثوق؟ | القاعدة المعمارية الصارمة |
| --- | --- | --- |
| `Authorization: Bearer <JWT>` | **موثوق بعد التحقق** | يُفحص توقيع RS256 مقابل المفتاح العام، ومطابقة `iss` و `aud` وتاريخ الانتهاء `exp`. |
| `X-Accounting-Signature` | **موثوق كدليل** | توقيع HMAC-SHA256 محسوب من نص قانوني منظم يربط الفاعل والشركات والطابع الزمني. |
| `company_id` في استعلام أو جسم الطلب | **غير موثوق إطلاقًا** | لا يمنح أي صلاحية؛ يجب أن يكون معرف الشركة عضوًا صريحًا ضمن `Principal.CompanyIDs`. |
| `actor_id` في جسم الطلب | **غير موثوق إطلاقًا** | يُهمل؛ الفاعل المعتمد في سجلات التدقيق هو المعرف المستخرج من التوقيع الموثق فقط. |
| `X-Source-System` المرسل من العميل | **غير موثوق** | يُستبدل بالقيمة الموثوقة `standalone-service` لمنع تزوير هوية الأنظمة المصدرية. |
