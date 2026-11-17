# مخطط سياق النظام (C4 Level 1 — System Context)

- **النطاق (Scope):** تموضع المحرك المحاسبي كصندوق أسود مع الأنظمة المحيطة والجهات الفاعلة.
- **مصدر الحقيقة (Source of Truth):** `cmd/accounting-service`, `pkg/sdk`, `pkg/spi`, `docs/architecture/service_boundary.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري

```mermaid
flowchart TB
    classDef actor fill:#e0f2fe,stroke:#0284c7,stroke-width:2px,color:#0f172a;
    classDef boundary fill:#f8fafc,stroke:#334155,stroke-width:2px,stroke-dasharray: 4 4,color:#0f172a;
    classDef engine fill:#eff6ff,stroke:#1d4ed8,stroke-width:3px,color:#0f172a;
    classDef external fill:#f1f5f9,stroke:#64748b,stroke-width:2px,color:#0f172a;
    classDef storage fill:#fef3c7,stroke:#d97706,stroke-width:2px,color:#0f172a;

    User["محاسب / مراجع مالي<br/>Financial Officer / Auditor"]:::actor
    APIClient["تطبيق أعمال / ERP / نظام فوترة<br/>ERP / Billing Host System"]:::actor

    subgraph Periphery ["البيئة المحيطة والحدود الخارجية"]
        Gateway["بوابة API والتحقق<br/>API Gateway / HMAC Proxy"]:::external
        IdP["مزود الهوية الموثوق<br/>Identity Provider (RS256 JWT)"]:::external
        EventBrokers["مصادر الأحداث الاقتصادية<br/>Kafka / RabbitMQ / Webhooks"]:::external
    end

    subgraph CoreEngineSystem ["نظام محرك المحاسبة (Accounting Engine System)"]
        Engine["محرك المحاسبة المزدوج<br/><b>Accounting Engine</b><br/>(Core Posting, Rules, Reports, Subledgers)"]:::engine
    end

    subgraph DataAndCompliance ["طبقة الحفظ والامتثال"]
        PostgresDB[("قاعدة بيانات PostgreSQL 18+<br/>RLS, WORM Triggers, Constraints")]:::storage
        WORMStorage[("سلسلة التدقيق وحفظ الدفاتر<br/>Immutable Audit Chain & WORM Log")]:::storage
        S3Bucket[("مستودع المرفقات والمستندات<br/>Object Store / Attachments")]:::storage
    end

    User -->|"استعراض القوائم وتدقيق القيود"| Gateway
    APIClient -->|"تسجيل الفواتير والأحداث الاقتصادية"| Gateway
    EventBrokers -->|"إرسال أحداث تجارية مع مفتاح تكرار"| Gateway

    Gateway -->|"تحقق الشهادات والتوقيع"| IdP
    Gateway -->|"طلب gRPC / HTTP مع Principal وسياق الشركة"| Engine
    APIClient -.->|"استدعاء مباشر في نمط المكتبة المدمجة (In-Process)"| Engine

    Engine -->|"معاملات ذرية وعزل شركات (SET LOCAL + RLS)"| PostgresDB
    Engine -->|"سلسلة بصمات SHA-256 وسجل العمليات المحاسبية"| WORMStorage
    Engine -->|"إرفاق مستندات الإثبات مع القيد"| S3Bucket

```

---

## 2. مفتاح المخطط (Legend)

| الرمز / اللون | المعنى | التفاصيل في الكود |
| --- | --- | --- |
| **الأزرق الفاتح** | جهات فاعلة بشرية وأنظمة أعمال | المحاسبون، أنظمة ERP، خدمات التجارة الإلكترونية |
| **الرمادي** | خدمات الحدود والمصادقة | Gateway الموقّع بـ HMAC، مزود الهوية JWT RS256 |
| **الأزرق الداكن** | نظام محرك المحاسبة الأساسي | `accounting_engine` (سواء كـ SDK مدمج أو كخدمة gRPC) |
| **الأصفر/البرتقالي** | مستودعات البيانات والامتثال | PostgreSQL (مُجبر بسياسات RLS)، وسلسلة التدقيق SHA-256 |
| **الخط المتقطع** | نمط المكتبة المدمجة (Embedded) | يتجاوز Gateway ويعمل داخل مساحة عناوين الـ Host process |

---

## 3. شروط التفاعل وقواعد الحدود

1. **لا ثقة في الهوية القادمة من المتصفح مباشرة:** الحد الخارجي هو بوابة موثوقة (`gateway-hmac`) أو رمز JWT معتمد من IdP مؤسسي.
2. **عزل الشركات الصارم:** كل استدعاء ملزم بتمرير `company_id` موثق، وتفرضه قاعدة البيانات آليًا عبر `app_current_company_id()`.
3. **منع التعديل (WORM):** لا تملك أي واجهة خارجية صلاحية حذف أو تعديل قيد مالي مرحل (`posted`).
