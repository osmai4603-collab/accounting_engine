# ARD-P6-02: تأمين حدود HTTP/gRPC عند تشغيل المحرك كخدمة مستقلة

- **المهمة:** `P6-02`
- **المرحلة:** `P6`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة
- **الملفات المعنية:** `internal/serviceauth/`، `cmd/accounting-service/boundary.go`، `cmd/accounting-service/grpc_boundary.go`، `cmd/accounting-service/config.go`، `cmd/accounting-service/server.go`، `cmd/accounting-service/grpc_server.go`، الاختبارات المرافقة، `docs/architecture/service_boundary.md`

## 1. السياق والمشكلة (Context & Problem)

عند تشغيل المحرك كخدمة مستقلة (`cmd/accounting-service`) تصبح واجهات HTTP/gRPC حدودًا شبكية مفتوحة. كانت الخدمة تقبل طلبات بلا أي إثبات هوية، وكانت `company_id` تُقرأ من الجسم أو الاستعلام وتُمرر إلى المحرك دون التحقق من أن المستدعي مخوّل بها. كما كانت `X-Source-System` القادمة من العميل تُعتمد كاسم نظام المصدر.

المطلوب في `P6-02`:

1. التحقق من هوية المستدعي وفق نمط نشر معتمد (Gateway موثوق أو JWT).
2. اشتقاق `ActorID` و`CompanyIDs` و`TenantID` من Claims/رؤوس موثوقة فقط.
3. تحويل الهوية الموثقة إلى `application.Principal` و`ExecutionContext`.
4. رفض انتحال Actor/Company قبل أي استدعاء للمحرك أو التخزين.
5. عدم فرض أي من ذلك على Embedded Library، وعدم إدخال Identity Provider أو User Management أو RBAC داخل Core.

## 2. القرارات والتنفيذ (Decisions & Implementation)

1. **حزمة مستقلة `internal/serviceauth` نقلية (Transport-Neutral):**
   - `Identity{ActorID, TenantID, CompanyIDs, Source}` يمثل الهوية الموثقة.
   - `Credentials{Authorization, Headers}` يجمع مدخلات HTTP وgRPC؛ البحث عن الرؤوس غير حساس لحالة الأحرف.
   - `Verifier` بواجهة واحدة: `Verify(ctx, Credentials) (Identity, error)`.
   - أخطاء مرجعية: `ErrUnauthenticated`، `ErrInvalidPrincipal`، `ErrMissingCredential`، إضافة إلى `ErrInvalidConfig`.
   - لا تُعاد أي مادة سرية (سر، رمز، توقيع) داخل رسائل الخطأ.

2. **نمط Gateway HMAC (`gateway-hmac`):**
   - توقيع HMAC-SHA256 على نص قانوني ثابت: الإصدار، Actor، Tenant، معرفات الشركات مرتبة، Unix seconds، Request ID.
   - التحقق من Actor غير الفارغ، ووجود UUID صالح واحد على الأقل، والطابع الزمني داخل نافذة `AUTH_CLOCK_SKEW` (افتراضي 60 ثانية)، ومقارنة ثابتة الزمن عبر `hmac.Equal`.
   - المدقق عديم الحالة؛ إعادة الإرسال خلال النافذة مسؤولية Gateway/Reverse Proxy وموثقة كحد معروف.

3. **نمط JWT (`jwt-hs256` / `jwt-rs256`):**
   - تنفيذ بمكتبة Go القياسية فقط (`crypto/hmac`, `crypto/rsa`, `crypto/x509`).
   - قائمة خوارزميات مسموحة صريحة؛ `alg=none` وأي خوارزمية غير مطابقة تُرفض قبل قراءة Claims.
   - التحقق من التوقيع أولًا، ثم `exp` (إلزامي) و`nbf` و`iat` مع هامش زمني، ثم `iss` مطابقة تامة و`aud`عضوية.
   - `sub` إلزامي → `ActorID`؛ الشركات من `company_ids` و/أو `company_id`، مع اشتراط UUID صالح واحد على الأقل.
   - تُتجاهل Claims الصلاحيات/Roles تمامًا.

4. **حد HTTP:**
   - Middleware يغطي `/api/v1/*` فقط؛ `/healthz` و`/readyz` و`/metrics` تبقى مفتوحة.
   - عند النجاح: يحقن `Principal` و`RequestMetadata` (Correlation ID من `X-Correlation-ID` ثم `X-Request-ID`، Idempotency Key من `Idempotency-Key`، و`SourceSystem=standalone-service`).
   - عند الفشل: `401` برسالة عامة `authentication required` ورأس `WWW-Authenticate` (`HMAC-SHA256` أو `Bearer`) قبل أي معالج.
   - قبل استدعاء المحرك في `entries` و`events` و`trial-balance`، تُقارن الشركة المطلوبة مع `Principal.CompanyIDs`؛ عدم التطابق → `403 company access denied`.
   - عند تعطيل المصادقة (تطوير/اختبار) لا يُحقن Principal ويبقى سلوك الرؤوس السابق كما هو.

5. **حد gRPC:**
   - `UnaryInterceptor` و`StreamInterceptor` (الأخير يحمي أيضًا Reflection) يستخدمان نفس `Verifier`.
   - بيانات الاعتماد من `metadata` الواردة؛ الفشل → `codes.Unauthenticated` برسالة عامة.
   - حقن `Principal` و`RequestMetadata` في السياق. `grpcRequestMetadata` تفضّل metadata المحقونة وتكمل الحقول الناقصة فقط.
   - كل معالجات `GetEntry` و`GetTrialBalance` و`GetBalanceSheet` و`GetIncomeStatement` و`ProcessEvent` و`PostEntry` تتحقق من الشركة المطلوبة؛ عدم التطابق → `codes.PermissionDenied`.
   - عند تفعيل الحد دون وجود Principal (استدعاء مباشر للمعالج) → `codes.Unauthenticated`.
   - `newGRPCServer(engine)` بقي كما هو لاختبارات الوضع بلا مصادقة، وأضيف `newGRPCServerWithVerifier(engine, verifier)`.

6. **Fail-Closed في الإعداد:**
   - إضافة `AuthMode`، `AuthSecret`، `AuthJWTPublicKey`، `AuthJWTIssuer`، `AuthJWTAudience`، `AuthClockSkew` إلى `Config`.
   - التحميل من `AUTH_MODE`، `AUTH_SECRET`، `AUTH_JWT_PUBLIC_KEY`، `AUTH_JWT_PUBLIC_KEY_FILE`، `AUTH_JWT_ISSUER`، `AUTH_JWT_AUDIENCE`، `AUTH_CLOCK_SKEW`.
   - في `production`: `AUTH_MODE` الصريح الآمن إلزامي؛ `none` أو الفارغ → `ErrProductionAuthRequired`، والنمط الآمن دون مادة صحيحة → `ErrInvalidAuthConfig`.
   - في `development`/`test`: التعطيل مسموح، لكن اختيار نمط آمن دون مادة صحيحة يفشل أيضًا.
   - فشل بناء المدقق أثناء التشغيل يعيد خطأ بدء تشغيل بدل تشغيل خدمة مفتوحة؛ ومسار `newHTTPServer` الاختباري يفشل مغلقًا بـ `503` ولا يخدم طلبات مجهولة.

## 3. جدول الإعدادات

| المتغير | القيم | إلزامي | ملاحظات |
| --- | --- | --- | --- |
| `AUTH_MODE` | `none` / `gateway-hmac` / `jwt-hs256` / `jwt-rs256` | في `production` | يقبل مرادفات مثل `gateway` و`hs256` و`RS256` |
| `AUTH_SECRET` | نص ≥ 16 بايت | `gateway-hmac` و`jwt-hs256` | لا يُسجَّل |
| `AUTH_JWT_PUBLIC_KEY` | PEM (PKIX/PKCS#1/CERTIFICATE) | `jwt-rs256` | RSA فقط |
| `AUTH_JWT_PUBLIC_KEY_FILE` | مسار ملف | بديل | يُقرأ عند غياب المتغير المباشر |
| `AUTH_JWT_ISSUER` | نص | موصى به | مطابقة تامة لـ `iss` |
| `AUTH_JWT_AUDIENCE` | نص | موصى به | عضوية في `aud` |
| `AUTH_CLOCK_SKEW` | `60s` أو `60` | لا | الافتراضي `60s` |

## 4. إثبات معايير القبول (Verification Evidence)

أوامر التحقق المنفذة:

```text
gofmt -w internal/serviceauth cmd/accounting-service
go test ./internal/serviceauth ./cmd/accounting-service -count=1
go vet ./internal/serviceauth/... ./cmd/accounting-service/...
```

النتائج:

```text
ok   github.com/osmai4603-collab/accounting_engine/internal/serviceauth   0.204s
ok   github.com/osmai4603-collab/accounting_engine/cmd/accounting-service 0.164s

go vet: نظيف (لا مخرجات)
```

| معيار القبول في الخطة | الدليل الاختباري | النتيجة |
| --- | --- | --- |
| لا تقبل الخدمة المستقلة طلبًا مجهولًا | `TestHTTPBoundaryRejectsMissingCredentials`، `TestGRPCBoundaryRejectsMissingMetadata` | محقق (`401` / `Unauthenticated`) |
| لا يُنشأ Execution Context من `actor_id`/`company_id` غير موثقين | `TestHTTPBoundaryInjectsVerifiedPrincipal`، `TestGatewayHMACVerifierRejectsMissingOrInvalidCredentials` (انتحال Actor/Company بعد التوقيع) | محقق |
| رفض انتحال الشركة قبل المحرك | `TestHTTPBoundaryRejectsCompanyImpersonation`، `TestHTTPBoundaryRejectsTrialBalanceImpersonation`، `TestGRPCBoundaryInjectsPrincipalAndRejectsForeignCompany`، `TestGRPCBoundaryRejectsForeignCompanyOnEvents` | محقق (`403` / `PermissionDenied` مع `postCalls/getCalls == 0`) |
| رفض التوقيع الخاطئ والطابع الزمني المنتهي | `TestHTTPBoundaryRejectsInvalidAndExpiredCredentials`، `TestGRPCBoundaryRejectsExpiredMetadata` | محقق |
| التحقق من JWT HS256/RS256 ورفض الرموز الفاسدة/المنتهية | `TestJWTVerifierHS256ValidToken`، `TestJWTVerifierRS256`، `TestJWTVerifierRejectsInvalidTokens` | محقق |
| عدم تسريب الأسرار/الرموز في الأخطاء | `assertNoCredentialLeak`، `TestInvalidJWTPublicKeyDoesNotLeakMaterial`، `TestAuthSecretNotLeakedInErrors` | محقق |
| إبقاء `healthz`/`readyz`/`metrics` مفتوحة | `TestHTTPBoundaryLeavesHealthReadinessAndMetricsOpen` | محقق |
| حماية Reflection في الوضع الآمن | `TestGRPCBoundaryProtectsReflectionStreams` | محقق (`Unauthenticated` بلا اعتماد) |
| Production Fail-Closed للمصادقة | `TestProductionDefaultAuthModeRejected`، `TestProductionExplicitNoneAuthModeRejected`، `TestProductionAuthMissingMaterialRejected` | محقق |
| Development يسمح بالتعطيل الصريح فقط | `TestDevelopmentAuthDisabledByDefault`، `TestDevelopmentSecureModeRequiresMaterial` | محقق |
| Embedded Library غير متأثر | `TestHTTPBoundaryDisabledPreservesEmbeddedBehaviour` + جميع اختبارات `cmd/accounting-service` السابقة | محقق |
| مسار gRPC بلا مصادقة يعمل كما قبل | `TestGRPCRegistersReflectsAndInvokesProtoServices` وغيرها عبر `newGRPCServer(engine)` | محقق |

ملاحظة: لم يُشغَّل `scripts/check_service_integration.sh` ضمن هذه المهمة لعدم توفر قاعدة PostgreSQL محلية جاهزة؛ السكربت يعمل بوضع `development` وبلا `AUTH_MODE`، أي أن مسار التعطيل الذي يحافظ عليه هذا التغيير هو نفس المسار المستخدم هناك.

## 5. الحدود (Limits)

- لا Identity Provider، ولا تدفق تسجيل دخول، ولا إدارة مستخدمين أو مجموعات أو أدوار.
- لا RBAC ولا سياسات صلاحيات تجارية داخل المحرك؛ `application.Authorizer` يبقى قدرة اختيارية يقدمها المضيف.
- المدقق بلا حالة: لا Nonce Cache ولا منع Replay مضمّن؛ نافذة الزمن تحد فقط، والمنع الفعلي مسؤولية Gateway/Reverse Proxy/Idempotency.
- عزل Tenant/Company على مستوى PostgreSQL RLS يبقى في `P6-03`؛ هذه المهمة تثبت الهوية والعضوية عند الحد فقط.
- تحسين عقود HTTP/gRPC (الحقول، الرموز، الإصدارات) يبقى للمهمتين `P6-04` و`P6-05`.
- mTLS وتقييد الشبكة وحذف رؤوس الهوية الواردة مسؤولية النشر والبنية التحتية، وليست داخل المحرك.
