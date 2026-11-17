# مخطط المراقبة والرصد وسياق التتبع (Observability & Context Propagation)

- **النطاق (Scope):** منظومة القياس والرصد المالي: تتبع الطلبات عبر `Correlation ID` و `Traceparent`، والمقاييس التشغيلية (Metrics)، والسجلات المنظمة (Structured Logging)، وتصنيف الأخطاء.
- **مصدر الحقيقة (Source of Truth):** `internal/observability`, `internal/posting/engine.go`, `internal/posting/error_classification.go`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: تدفق سياق المراقبة (Observability Pipeline)

```mermaid
flowchart LR
    classDef client fill:#f8fafc,stroke:#475569,stroke-width:2px,color:#0f172a;
    classDef edge fill:#fef3c7,stroke:#d97706,stroke-width:2px,color:#0f172a;
    classDef core fill:#eff6ff,stroke:#2563eb,stroke-width:2px,color:#0f172a;
    classDef obs fill:#ecfdf5,stroke:#059669,stroke-width:2px,color:#0f172a;

    ClientReq["طلب العميل الخارجي<br/>Headers:<br/>X-Correlation-ID / X-Request-ID<br/>traceparent (W3C Trace Context)"]:::client

    subgraph ServiceEdge ["حد الخدمة (Service Edge)"]
        EdgeMiddleware["معالج التتبع والربط<br/><code>observability.EnsureTraceparent(ctx)</code><br/>حقن CorrelationID و RequestMetadata"]:::edge
    end

    subgraph CoreEnginePosting ["محرك الترحيل (Core Posting)"]
        ContextEnrichment["إثراء سياق المعاملة (Context Attributes)<br/>WithCompanyID, WithPeriodID, WithEntryID"]:::core
        LatencyTimer["مؤقت زمن المعاملة والترحيل<br/><code>time.Since(start)</code>"]:::core
        ErrorClassifier["تصنيف الأخطاء المحاسبية<br/><code>ClassifyError(err)</code><br/>Code + Category (e.g. CONFLICT, AUTH)"]:::core
    end

    subgraph ObservabilitySinks ["مستقبلات المراقبة والتحليل (Telemetry Sinks)"]
        MetricsRegistry["سجل المقاييس (Prometheus Metrics)<br/>- <code>posting_attempts_total</code><br/>- <code>posted_entries_total</code><br/>- <code>posting_duration_seconds</code><br/>- <code>transaction_duration_seconds</code>"]:::obs
        StructuredLogs["السجلات المنظمة (slog.Logger JSON)<br/>- company_id, journal_id, period_id<br/>- duration_seconds, error_code, error_category"]:::obs
        OpenTelemetry["نظام التتبع الموزع (OTel Traces)<br/>ربط Span المعاملة بـ W3C Traceparent"]:::obs
    end

    ClientReq --> EdgeMiddleware
    EdgeMiddleware --> ContextEnrichment
    ContextEnrichment --> LatencyTimer
    LatencyTimer --> ErrorClassifier

    LatencyTimer --> MetricsRegistry
    ErrorClassifier --> MetricsRegistry
    ErrorClassifier --> StructuredLogs
    ContextEnrichment --> OpenTelemetry
```

---

## 2. جدول مقاييس الأداء المحاسبي (Key Financial Metrics)

| اسم المقياس (Metric Name) | النوع (Type) | وسوم القياس (Labels) | الاستخدام التشغيلي والإنذارات |
|---|---|---|---|
| `accounting_posting_attempts_total` | Counter | `company_id`, `journal_id` | تتبع إجمالي محاولات الترحيل والتنبيه عند ارتفاع معدل الطلبات. |
| `accounting_posted_entries_total` | Counter | `company_id`, `classification` | قياس عدد العمليات المحاسبية الناجحة ومعدل نمو القيود. |
| `accounting_posting_duration_seconds` | Histogram | `operation`, `status` | مراقبة كمون الترحيل والتنبيه عند تجاوز العمليات لحاجز الـ 500ms. |
| `accounting_transaction_duration_seconds`| Histogram | `isolation_level` | قياس مدة حجز أقفال المعاملات في PostgreSQL لتفادي بطء الحوض. |
| `accounting_posting_errors_total` | Counter | `error_code`, `category` | تفريق أخطاء تعارض التزامن عن أخطاء خرق القواعد المالية. |

