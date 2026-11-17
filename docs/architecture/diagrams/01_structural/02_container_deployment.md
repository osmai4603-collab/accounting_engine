# مخطط الحاويات ونمط النشر (Container & Deployment View)

- **النطاق (Scope):** المقارنة المعمارية بين نمط **المكتبة المدمجة (Embedded Library)** ونمط **الخدمة المستقلة (Standalone Service)**.
- **مصدر الحقيقة (Source of Truth):** `cmd/accounting-service`, `pkg/sdk`, `internal/serviceauth`, `docs/architecture/service_boundary.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري

```mermaid
flowchart TB
    classDef host fill:#f8fafc,stroke:#475569,stroke-width:2px,color:#0f172a;
    classDef embedded fill:#e0f2fe,stroke:#0369a1,stroke-width:2px,color:#0f172a;
    classDef standalone fill:#ecfdf5,stroke:#059669,stroke-width:2px,color:#0f172a;
    classDef shared fill:#fef3c7,stroke:#d97706,stroke-width:2px,color:#0f172a;

    subgraph ModeA ["النمط الأول: مكتبة مدمجة داخل العملية (Embedded Library Mode)"]
        subgraph HostProcess ["عملية المضيف (Host Go Binary)"]
            HostApp["كود تطبيق المضيف<br/>ERP / Billing Engine"]:::host
            PrincipalBuilder["منشئ Principal المحلي<br/>(In-Process Context)"]:::host
            SDK["حزمة SDK المحاسبية<br/><code>pkg/sdk.Builder</code>"]:::embedded
            DomainCoreA["المحرك المحاسبي الداخلي<br/><code>internal/posting</code>, <code>reporting</code>"]:::embedded
            MemAdapter["محول الذاكرة الاختياري<br/><code>internal/adapters/memory</code>"]:::embedded

            HostApp --> PrincipalBuilder
            PrincipalBuilder -->|تمرير عبر context.Context| SDK
            SDK --> DomainCoreA
            DomainCoreA -.->|اختبارات سريعة| MemAdapter
        end
    end

    subgraph ModeB ["النمط الثاني: خدمة مستقلة عبر الشبكة (Standalone Service Mode)"]
        subgraph ServiceProcess ["عملية خدمة المحاسبة (cmd/accounting-service Binary)"]
            NetworkEdge["واجهات الشبكة<br/>gRPC (HTTP/2) + HTTP/JSON REST"]:::standalone
            AuthVerifier["التحقق الأمني والمصادقة<br/><code>internal/serviceauth</code><br/>(HMAC Gateway / RS256 JWT)"]:::standalone
            PrincipalMapper["محول الهوية وسياق الشركات<br/>(Enforces Principal.CompanyIDs)"]:::standalone
            SDK_B["واجهة المحرك الموحدة<br/><code>pkg/sdk.Engine</code>"]:::standalone
            DomainCoreB["المحرك المحاسبي الداخلي<br/>(Posting, Rules, Reporting)"]:::standalone

            NetworkEdge --> AuthVerifier
            AuthVerifier --> PrincipalMapper
            PrincipalMapper -->|WithPrincipal & Company| SDK_B
            SDK_B --> DomainCoreB
        end
    end

    subgraph DataTier ["مستوى البيانات والامتثال المشترك"]
        PostgresDriver["محول تخزين PostgreSQL<br/><code>internal/adapters/postgres</code>"]:::shared
        PG_Pool[("حوض اتصالات pgxpool.Pool<br/>(عزل RLS لكل معاملة)")]:::shared
    end

    DomainCoreA --> PostgresDriver
    DomainCoreB --> PostgresDriver
    PostgresDriver --> PG_Pool
```

---

## 2. المقارنة التفصيلية بين النمطين

| الخاصية | نمط المكتبة المدمجة (Embedded Library) | نمط الخدمة المستقلة (Standalone Service) |
| --- | --- | --- |
| **العملية التنفيذية (Process)** | داخل نفس عملية المضيف (`In-Process memory`) | عملية مستقلة ومنفصلة عبر الشبكة (`Out-of-Process`) |
| **بروتوكول التواصل** | استدعاء دوال Go مباشر (`In-memory function calls`) | gRPC (Protobuf v1) أو HTTP/JSON REST |
| **المصادقة والتفويض** | لا وجود للمصادقة؛ المضيف يبني `application.Principal` بنفسه | إجبارية بالإنتاج: `gateway-hmac` أو `jwt-rs256` أو `jwt-hs256` |
| **الكمون (Latency)** | أقل من 1 ملي ثانية (صفر تكلفة شبكة) | تكلفة فك تشفير الشبكة والتسلسل (gRPC/HTTP overhead) |
| **الاعتماديات البرمجية** | يتطلب أن يكون المضيف مكتوبًا بلغة Go | حيادي تجاه لغات البرمجة (يمكن استدعاؤه من Python, Java, Node) |
| **حالة الفشل** | انهيار المضيف يوقف المحرك والعكس | عزل كامل للعمليات؛ فشل المضيف لا يؤثر على خدمة المحاسبة |
| **عزل المستأجرين (RLS)** | يفرضه المحول عبر `SET LOCAL app.current_company_id` | نفس الضمانة تمامًا على مستوى المعاملة بقاعدة البيانات |

---

## 3. محددات الأمان في وضع الخدمة المستقلة

1. **الرفض التلقائي للوضع غير الآمن:** يفشل إقلاع الخدمة فورًا في بيئة `APP_ENV=production` إذا كان `AUTH_MODE=none` أو فارغًا.
2. **منع حقن هوية زائفة:** لا يُعتد بأي `actor_id` أو `company_id` موجود في جسم الطلب (JSON body) لأغراض التفويض؛ بل يُستخرج حصرًا من ترويسات التوقيع المعتمدة (`Principal`).
