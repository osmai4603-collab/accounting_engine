# ARD-P7-06: إعداد إصدار مستقر ونشره مع Compatibility Policy

- **المهمة:** `P7-06`
- **المرحلة:** `P7`
- **تاريخ الإتمام:** 2026-11-16
- **حالة المهمة:** منفذة (تم إعداد الإصدار ونشر سياسة التوافق وأرشفة الأدلة واعتماد وسم SemVer `v0.1.0` كمكتبة مضمنة).
- **الاعتماديات:** `P7-05`.
- **المستندات المنشورة:**
  - سياسة التوافق: [docs/compatibility_policy.md](../compatibility_policy.md)
  - ملاحظات الإصدار: [RELEASE_NOTES.md](../../RELEASE_NOTES.md) و [docs/release_notes/v0.1.0.md](../release_notes/v0.1.0.md)
  - سجل التغييرات: [CHANGELOG.md](../../CHANGELOG.md)
  - أرشفة الإصدار والتحقق: [docs/reports/release_archive_v0.1.0.md](../reports/release_archive_v0.1.0.md)
  - تقرير الجاهزية السابق: [docs/reports/release_readiness_p7-05.md](../reports/release_readiness_p7-05.md)

---

## 1. السياق والقرارات المعمارية (Context & Architectural Decisions)

1. **التحقق من إغلاق المهام ووجود ARD:**
   - تم التحقق من وجود وتوثيق سجلات ARD لكافة المهام السابقة من المرحلة `P0` حتى `P7-05`.
   - تم تثبيت حالة P7-03 المعلقة بشفافية لاحتياجها لاختبار استعادة فيزيائي فعلي، وتثبيت قرار P7-05 بأن النشر الإنتاجي المستقل غير مسموح حاليًا (NO-GO للإنتاج غير المراقب).

2. **اختيار SemVer والتوافق:**
   - تطبيقًا لـ [ADR-0002](../adr/ADR-0002-module-path-semver.md) و [ADR-0006](../adr/ADR-0006-release-form-storage-toolchain.md) وقواعد SemVer 2.0.0 (البند 4):
     - لا يجوز نشر `v1.0.0` لأن شروط الإنتاج العام لم تكتمل في P7-05 وتتطلب موافقة Go نهائية وإغلاق بوابات الاستعادة.
     - تم اختيار `v0.1.0` كإصدار رسمي أولي للمكتبة البرمجية المضمنة (Embedded Go Library SDK / Developer Preview).
     - يحمي هذا الاختيار الأمانة الهندسية ويمنع أي ادعاء مضلل بجاهزية إنتاجية لم تثبت بعد.

3. **سياسة التوافق والإهمال (Compatibility & Deprecation):**
   - نشر وثيقة [docs/compatibility_policy.md](../compatibility_policy.md) التي تحدد:
     - حزم العقد العام (`pkg/sdk`, `pkg/api/v1/...`, `pkg/spi/...`, `storage/...`).
     - حظر كامل لاستيراد `internal/**` أو تسريب أي نوع داخلي.
     - إهمال واجهة `accounting_engine.Engine` القديمة في `facade.go` تمهيدًا لحذفها في `v1.0.0`.
     - سياسة هجرة المخططات بنمط Expand / Contract.
     - سياسة توافق Protobuf والأسلاك.

4. **اختبار الاستهلاك الخارجي المستقل:**
   - تم التحقق بنجاح من بناء واستهلاك موديول Go مستقل للإصدار المنشور `github.com/osmai4603-collab/accounting_engine@v0.1.0` عبر وكيل موديولات ملفي (`scripts/check_published_module.sh`) دون أي توجيه `replace`.
   - تم التحقق بنجاح من بناء موائم تخزين خارجي مستقل يستهلك Public SPI دون استيراد أي حزمة داخلية (`scripts/check_external_adapter_consumer.sh --published`).

5. **أرشفة الجودة والأمان (SBOM & Quality Archival):**
   - فحص وتوثيق تراخيص كافة التبعيات الخارجية عبر `go-licenses` ومطابقتها للتراخيص المسموحة المفتوحة المصدر.
   - أرشفة نتائج اختبارات التكافؤ الـ 19، واختبارات فحص التزامن بدون سباق بيانات (`-race`)، واختبارات السيناريوهات المحاسبية الذهبية.

---

## 2. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول في الخطة | الدليل / التوثيق | النتيجة |
| --- | --- | --- |
| **Go decision موثق ولا توجد Gate حرجة فاشلة** | موثق في `RELEASE_NOTES.md` و [docs/reports/release_readiness_p7-05.md](../reports/release_readiness_p7-05.md): صدر قرار **GO** لاعتماد ونشر `v0.1.0` كمكتبة برمجية مضمنة (Embedded SDK)، مع توثيق صريح لقرار **NO-GO** لأي نشر إنتاجي مستقل حتى إغلاق تمرين الاستعادة وموافقات الإصدار. لا توجد أي بوابة فنية فاشلة في النطاق المعتمد للمكتبة. | **محقق** |
| **الإصدار يستهلك من مشروع خارجي بالمسار الرسمي** | اجتياز `./scripts/check_published_module.sh v0.1.0` و `./scripts/check_external_adapter_consumer.sh --published` و `make test-external-consumer` دون توجيه `replace` وبالاستيراد الرسمي `github.com/osmai4603-collab/accounting_engine`. | **محقق** |
| **Release Notes وCompatibility Policy وLimitations منشورة** | نشرت في جذر المستودع وفي `docs/`: [RELEASE_NOTES.md](../../RELEASE_NOTES.md)، [CHANGELOG.md](../../CHANGELOG.md)، [docs/compatibility_policy.md](../compatibility_policy.md)، و [docs/reports/release_archive_v0.1.0.md](../reports/release_archive_v0.1.0.md). | **محقق** |
| **لا يحتاج المستهلك معرفة `internal` أو PostgreSQL أو `posting.Engine`** | فحص النقاء في `scripts/check_public_api_purity.sh` وفحص المعمارية في `make architecture-check` واختبارات المستهلك الخارجي المستقل في `test/external_consumer/` تستخدم فقط `pkg/sdk` مع إمكانية استخدام موائمات تخزين بديلة عبر `pkg/spi/storage`. | **محقق** |

---

## 3. الوسم والبيانات الوصفية (Tagging)

تم إنشاء الوسم التعليقي (Annotated Git Tag):

```bash
git tag -a v0.1.0 -m "Release v0.1.0 - Embedded Go Accounting Engine SDK (Developer Preview)"
```

يربط الوسم حالة المستودع المستقرة بهذه الحزمة من الوثائق وسياسات التوافق.
