# ARD-P8-05: فرض سلامة Bootstrap وتحقق الاعتماديات (Capability Integrity)

- **المهمة:** `P8-05`
- **المرحلة:** `P8`
- **تاريخ الإتمام:** 2026-11-17
- **حالة المهمة:** منفذة.
- **الاعتماديات:** `P8-01`.
- **الأدلة الرئيسية:**
  - منطق التحقق: [`pkg/sdk/bootstrap_capabilities.go`](../../pkg/sdk/bootstrap_capabilities.go)
  - ترتيب التنفيذ في الباني: [`pkg/sdk/builder.go`](../../pkg/sdk/builder.go)
  - اختبارات الفشل المبكر: [`test/bootstrap/capability_integrity_test.go`](../../test/bootstrap/capability_integrity_test.go)
  - مصفوفة الاعتماديات: [`docs/architecture/bootstrap_capability_matrix.md`](../architecture/bootstrap_capability_matrix.md)

---

## 1. السياق والقرارات المعمارية (Context & Architectural Decisions)

1. **المشكلة:**
   - كان بإمكان المضيف بناء محرك "يعمل جزئياً": يطلب التقارير دون `ReportingStore`، أو وحدات معايير دون منافذ كاملة، أو استقبال أحداث دون مخازن عدم التكرار.
   - النتيجة كانت فشلاً وقت التشغيل بدلاً من فشل عند التهيئة.

2. **القاعدة المعمارية:**
   - `Enabled(C) ⟹ Available(Service_C)`.
   - تُعبَّر "C مفعّلة" صراحةً عبر `WithRequiredCapabilities(C)`، وهي **تحقق** لا **تفعيل**: لا تضيف أي خدمة من تلقاء نفسها.

3. **رموز القدرات:**
   ```go
   sdk.BootstrapReporting      // "reporting"
   sdk.BootstrapGovernance     // "governance"
   sdk.BootstrapStandards      // "standards"
   sdk.BootstrapEventIngestion // "event_ingestion"
   sdk.BootstrapSubledgers     // "subledgers"
   sdk.BootstrapPostingPolicy  // "posting_policy"
   ```

4. **التحقق قبل أي أثر جانبي:**
   - يجري `validateRequiredCapabilities()` داخل `preflight()` بعد التحقق من مصدر التخزين وبنيوية المنافذ، وقبل بناء طبقة التطبيق وتهيئة الوحدات.
   - تُجمع كل الأخطاء وتُعاد معاً، فلا يُصلح المطور خطأً ليكتشف التالي.

5. **رسائل قابلة للتنفيذ:**
   - كل رسالة تذكر اسم القدرة، الاعتمادية الناقصة، والخيار أو المنفذ الذي يحلها.
   - مثال: `bootstrap: capability "reporting" is required but unavailable: configure WithReporting(store) or supply Balance, MasterData and Entries ports`.

6. **قدرات غير متاحة على مسار المنافذ:**
   - `subledgers` غير ممثلة في `storage.Ports`؛ لذلك ترفض صراحةً على مسار `WithStoragePorts` مع توجيه لاستخدام `WithCoreStorage`، بدلاً من الادعاء الكاذب بالتوفر.

7. **أسماء غير معروفة:**
   - `WithRequiredCapabilities("not-a-capability")` يفشل فوراً قبل `Build`، فلا تمر الأخطاء المطبعية بصمت.

---

## 2. مصفوفة الاعتماديات المختصرة

| القدرة | مسار `WithCoreStorage` | مسار `WithStoragePorts` |
| --- | --- | --- |
| `reporting` | `WithReporting(store)` | `Balance` + `MasterData` + `Entries` |
| `governance` | `WithGovernance(store)` | `Audit` |
| `standards` | `WithStandard(module)` | `WithStandard(module)` + `Entries` + `MasterData` |
| `event_ingestion` | مخزن يدعم `PublicEventIdempotencyStore` و`PublicEventMappingStore` | `Events.Idempotency` + `Events.Mappings` |
| `subledgers` | مخزن يدعم `subledgers.Repository` | غير متاح (يرفض صراحةً) |
| `posting_policy` | `RequirePolicy` + مخزن يدعم `policy.Repository` | `RequirePolicy` + `PolicyUnit` في قدرات الترحيل |

التفاصيل الكاملة ورسائل الأخطاء في [مصفوفة اعتماديات Bootstrap](../architecture/bootstrap_capability_matrix.md).

---

## 3. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| فشل صريح عند طلب Reporting دون مخزن | `TestBootstrap_ReportingRequiredWithoutStoreFailsFast` | **محقق** |
| فشل عند طلب وحدات معايير دون منافذ كافية | `TestBootstrap_StandardsRequiredWithoutModuleFailsFast` و`TestBuilder_PortsModeSupportsStandards` | **محقق** |
| عدم بدء المحرك بحالة "يعمل جزئياً" | كل التحققات تجري في `preflight` قبل بناء طبقة التطبيق وقبل `Initialize`؛ اختبار `TestBootstrap_SatisfiedCapabilitiesBuild` يثبت أن التهيئة الكاملة تنجح فقط عند اكتمال الاعتماديات | **محقق** |
| توثيق مصفوفة الاعتماديات | [`docs/architecture/bootstrap_capability_matrix.md`](../architecture/bootstrap_capability_matrix.md) | **محقق** |
| بقاء حدود طبقة التطبيق | `make architecture-check` يمر: فحص سياسة الترحيل يتم عبر `sdkapplication.HasPrivatePostingPolicy` دون أن يستورد `pkg/sdk` حزمة `internal/posting` مباشرة | **محقق** |

---

## 4. ملاحظات وحدود

- `WithRequiredCapabilities` لا يستبدل `WithReporting` أو `WithStandard` أو أي خيار تفعيل آخر؛ هو يتحقق فقط.
- متطلبات وحدات المعايير الخاصة (مثل حاجة `ifrs9` لقراءة أرصدة) تبقى في `ValidateDependencies(ModulePorts)` وتفشل بعد نجاح فحص القدرات العامة.
- أي قدرة جديدة يجب أن تُضاف إلى: الثابت، `valid()`، `validateRequiredCapabilities`، المصفوفة، واختبار فشل مبكر.
