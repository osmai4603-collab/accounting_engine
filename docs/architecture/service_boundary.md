# حدود خدمة المحاسبة المستقلة — المصادقة والهوية الموثوقة

- **النطاق:** `cmd/accounting-service` و`internal/serviceauth` فقط.
- **المهمة:** `P6-02` (تأمين حدود HTTP/gRPC عند تشغيل المحرك كخدمة مستقلة).
- **المراجع:** `ADR-0004`، `ARD-P6-01`، `ARD-P6-02`.
- **التاريخ:** 2026-11-14.

## 1. المبدأ

المحرك في وضع **Embedded Library** لا يعرف شيئًا عن هذه الوثيقة: لا Middleware، ولا Interceptor، ولا Identity Provider، ولا إدارة مستخدمين، ولا RBAC. المضيف يبني `application.Principal` بنفسه ويمرره عبر `context.Context`.

هذه الوثيقة تخص فقط **Standalone Service** الاختياري. فيه تصبح واجهات HTTP/gRPC حدودًا شبكية، لذلك:

1. يُتحقق من هوية المستدعي عند الحد (Transport Boundary) فقط.
2. تُحوَّل الهوية الموثقة إلى `Principal`، ثم إلى `ExecutionContext` موحد.
3. لا يُقبل `actor_id` أو `company_id` من جسم الطلب كونه مصدر تفويض.
4. لا يُضاف Authorization Model تجاري، ولا Roles/Permissions داخل Core Ledger.

## 2. أنماط النشر

| النمط | `AUTH_MODE` | المادة المطلوبة | الاستخدام |
| --- | --- | --- | --- |
| معطّل صراحة | `none` أو فارغ | لا شيء | `development` / `test` فقط |
| Gateway موثوق | `gateway-hmac` | `AUTH_SECRET` (≥ 16 بايت) | الخدمة خلف Gateway يوقّع هوية المستدعي |
| JWT بمفتاح مشترك | `jwt-hs256` | `AUTH_SECRET` (≥ 16 بايت) | بيئات داخلية تُصدر JWT موقّعًا بـ HS256 |
| JWT بمفتاح عام | `jwt-rs256` | `AUTH_JWT_PUBLIC_KEY` أو `AUTH_JWT_PUBLIC_KEY_FILE` | IdP خارجي يوقّع RS256 |

في `production` **يُرفض** `AUTH_MODE` الفارغ أو `none`، ويفشل التشغيل قبل فتح أي منفذ. في `development`/`test` يُسمح بالتعطيل الصريح كي تبقى سكربتات التكامل ومسارات العمل المحلية تعمل، لكن اختيار نمط آمن دون مادة صحيحة يفشل أيضًا.

## 3. الإعدادات

| المتغير | إلزامي | الوصف |
| --- | --- | --- |
| `APP_ENV` | نعم في الإنتاج | `production` / `development` / `test` |
| `AUTH_MODE` | نعم في الإنتاج | `gateway-hmac` أو `jwt-hs256` أو `jwt-rs256` |
| `AUTH_SECRET` | حسب النمط | سر مشترك لـ `gateway-hmac` و`jwt-hs256`، لا يُسجَّل ولا يظهر في الأخطاء |
| `AUTH_JWT_PUBLIC_KEY` | `jwt-rs256` | مفتاح RSA عام بصيغة PEM (PKIX أو PKCS#1 أو شهادة) |
| `AUTH_JWT_PUBLIC_KEY_FILE` | بديل اختياري | مسار ملف PEM؛ يُقرأ فقط عند غياب `AUTH_JWT_PUBLIC_KEY` |
| `AUTH_JWT_ISSUER` | موصى به | مطابقة تامة لـ `iss` إن حُدد |
| `AUTH_JWT_AUDIENCE` | موصى به | يجب أن يظهر ضمن `aud` إن حُدد |
| `AUTH_CLOCK_SKEW` | لا | مدة Go (`60s`) أو ثوانٍ (`60`)؛ الافتراضي `60s` |

لا تُطبع `AUTH_SECRET` ولا محتوى المفتاح ولا التوقيعات في السجلات أو رسائل الخطأ. أخطاء بدء التشغيل تصف النقص أو الصيغة فقط.

## 4. المدخلات الموثوقة وغير الموثوقة

| المدخل | موثوق؟ | القاعدة |
| --- | --- | --- |
| `Authorization: Bearer <JWT>` | بعد التحقق | يُتحقق التوقيع و`alg` و`exp`/`nbf`/`iat` و`iss` و`aud` |
| `X-Accounting-Actor-ID` | فقط مع توقيع Gateway صحيح | لا يُقبل وحده |
| `X-Accounting-Company-IDs` | فقط مع توقيع Gateway صحيح | UUID صالح واحد على الأقل |
| `X-Accounting-Tenant-ID` | فقط مع توقيع Gateway صحيح | اختياري |
| `X-Accounting-Timestamp` | موثوق فقط ضمن التوقيع | RFC3339 أو Unix seconds |
| `X-Accounting-Signature` | إثبات | HMAC-SHA256 hex |
| `X-Correlation-ID` / `X-Request-ID` | غير هوية | تُمرر للتتبع بعد التحقق أو تبقى كما هي في وضع التعطيل |
| `Idempotency-Key` | غير هوية | يُمرر إلى Application Layer |
| `X-Source-System` من العميل | **غير موثوق في الوضع الآمن** | يُستبدل بـ `standalone-service` |
| `company_id` في الجسم/الاستعلام | **غير موثوق للتفويض** | يجب أن يكون ضمن `Principal.CompanyIDs` |
| `actor_id` في الجسم | **غير موثوق** | لا يُستخدم لبناء Principal |

## 5. اشتقاق Execution Context

1. يستخرج الحد بيانات الاعتماد من رؤوس HTTP أو Metadata الخاصة بـ gRPC.
2. يستدعي `serviceauth.Verifier.Verify` ويحصل على `Identity{ActorID, TenantID, CompanyIDs, Source}`.
3. يحوّل الهوية إلى `publicapplication.Principal` ويحقنها بـ `publicapplication.WithPrincipal`.
4. يحقن `RequestMetadata`:
   - `CorrelationID` من `X-Correlation-ID` ثم `X-Request-ID`.
   - `IdempotencyKey` من `Idempotency-Key`.
   - `SourceSystem = "standalone-service"` (قيمة يفرضها الحد).
5. قبل استدعاء المحرك، يتحقق المعالج من أن الشركة المطلوبة عضو في `Principal.CompanyIDs`؛ وإلا يُرفض الطلب.
6. يقرأ SDK الـ `Principal` من `context.Context` ويبني `application.Context` الداخلي. لا يمرر الحد أي تفويض إضافي، ويبقى `application.Authorizer` قدرة اختيارية لدى المضيف.

## 6. توقيع Gateway HMAC

الرؤوس:

```
X-Accounting-Actor-ID: <actor>
X-Accounting-Company-IDs: <uuid-1>,<uuid-2>
X-Accounting-Tenant-ID: <tenant>            (اختياري)
X-Accounting-Timestamp: <RFC3339|unix>      
X-Accounting-Request-ID: <request-id>       (اختياري)
X-Accounting-Signature: <hex hmac-sha256>
```

النص القانوني الموقّع (سطور مفصولة بـ `\n`):

```
accounting-gateway-hmac-v1
<actor>
<tenant or empty>
<company ids sorted ascending, comma separated, lower-case UUID>
<unix seconds>
<request id or empty>
```

قواعد التحقق:

- `actor` غير فارغ وبطول محدود.
- شركة UUID صالحة واحدة على الأقل؛ أي قيمة غير UUID ترفض الطلب.
- تُرتَّب معرفات الشركات قبل الحساب؛ التكرارات لا تغيّر الهوية النهائية لكنها تبقى ضمن النص الموقّع كما أرسلها Gateway.
- إعادة استخدام `request_id` أو عدم إرساله لا يخلّان بالتوقيع، لكنهما لا يمنعان إعادة الإرسال.
- المقارنة تتم بـ `hmac.Equal` (ثابتة الزمن).
- الطابع الزمني يجب أن يقع داخل `AUTH_CLOCK_SKEW` (افتراضي 60 ثانية)؛ المفقود أو المنتهي أو المستقبلي يُرفض.

**حد إعادة الإرسال (Replay):** المدقق عديم الحالة ولا يحتفظ بـ Nonce Cache. نافذة الطابع الزمني تقلل فقط من عمر إعادة الإرسال. يجب على Gateway أو Reverse Proxy أو طبقة Idempotency إعادة منع Replay خلال النافذة، خصوصًا للعمليات المالية.

## 7. JWT

- التنسيق: Compact JWT بثلاثة أجزاء، بحد أقصى 16 KiB.
- الخوارزميات المسموحة: `HS256` أو `RS256` فقط، ويجب أن تطابق `AUTH_MODE`. `alg=none` أو أي خوارزمية أخرى تُرفض قبل قراءة الـ Claims.
- التحقق من التوقيع يسبق تفسير الـ Claims.
- `exp` إلزامي. `nbf` و`iat` يُفحصان إن وُجدا مع هامش `AUTH_CLOCK_SKEW`.
- `iss` مطابقة تامة عند ضبط `AUTH_JWT_ISSUER`.
- `aud` قد تكون String أو Array؛ يجب أن تحتوي `AUTH_JWT_AUDIENCE` عند ضبطها.
- `sub` إلزامي وغير فارغ ويصبح `ActorID`.
- الشركات من `company_ids` (مصفوفة) و/أو `company_id` (نص)، ويجب أن تكون UUID صالحة، وواحدة على الأقل.
- `tenant_id` اختياري ويصبح `TenantID`.
- `permissions` أو أي Roles Claims تُتجاهل تمامًا؛ التفويض التجاري ليس مسؤولية المحرك.

## 8. خريطة الأخطاء

| الحالة | HTTP | gRPC | الرسالة |
| --- | --- | --- | --- |
| لا بيانات اعتماد / توقيع خاطئ / رمز منتهٍ / طابع زمني خارج النافذة | `401` + `WWW-Authenticate` | `Unauthenticated` | `authentication required` |
| هوية صالحة لكن الشركة المطلوبة خارج `Principal.CompanyIDs` | `403` | `PermissionDenied` | `company access denied` |
| جسم/معاملات غير صالحة | `400` | `InvalidArgument` | رسالة تحقق عامة |
| فشل داخلي أو تخزين | `500` / `503` حسب التصنيف | `Internal` / `Unavailable` | رسالة عامة بلا سبب داخلي |
| إعداد أمني غير صالح عند بناء الخادم | `503` لكل الطلبات (Fail-Closed) أو خطأ بدء تشغيل | خطأ بدء تشغيل | وصف النقص دون أسرار |

`/healthz` و`/readyz` و`/metrics` تبقى مفتوحة في كل الأنماط حتى تعمل الفحوص وتجمع المقاييس. المنطق `api/v1/*` وحده محمي.

## 9. افتراضات منع التجاوز (Anti-Bypass)

1. يجب أن يكون منفذ الخدمة متاحًا فقط لـ Gateway/Reverse Proxy الموثوق عبر الشبكة أو mTLS أو Service Mesh. هذا **مسؤولية النشر** وليست مسؤولية المحرك.
2. على Gateway **حذف** أي رؤوس `X-Accounting-*` قادمة من العميل قبل إضافة هويته الموقعة. عدم الحذف يسمح بتضارب الرؤوس، رغم أن التوقيع يمنع التزوير دون معرفة السر.
3. السر المشترك وملف المفتاح العام يجب أن يُدارا خارج الصورة (Secret Manager / Kubernetes Secret) وبأذونات قراءة محدودة.
4. في نمط JWT، أي حامل لرمز صالح يمكنه استدعاء الخدمة؛ الحد لا يفرض mTLS. من يحتاج تقييد الشبكة يضيفه في البنية التحتية.
5. تغيير `AUTH_MODE` أو المادة يستلزم إعادة تشغيل آمنة؛ لا يوجد Hot Reload في هذه المهمة.
6. لا يُوثق بأي `company_id` أو `actor_id` قادم من العميل. المقارنة دائمًا مع `Principal` الموثق.

## 10. ما لا تفعله هذه المهمة

- لا Identity Provider ولا تدفق تسجيل دخول.
- لا إدارة مستخدمين أو مجموعات أو أدوار.
- لا RBAC ولا سياسات صلاحيات للشركة أو العملية.
- لا تغيير في Posting Core أو Application Layer أو SDK.
- `application.Authorizer` يبقى قدرة اختيارية يقدمها المضيف؛ غيابه لا يفتح تفويضًا إضافيًا، ووجوده لا يُستبدل بفحص العضوية الأساسي.
- تحسين عقود HTTP/gRPC التفصيلية يبقى للمهمتين `P6-04` و`P6-05`.
