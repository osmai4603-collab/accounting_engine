# أرشيف الوثائق التاريخية (Historical Documentation Archive)

يحتوي هذا المجلد على الوثائق والتقارير والمسودات السابقة التي تم إعدادها خلال المراحل الأولى من المشروع أو خلال تقييمات سابقة.

> [!WARNING]
> **تنبيه إخلاء مسؤولية ومعيارية:**
> الوثائق الموجودة في هذا المجلد هي لأغراض **التوثيق التاريخي والرجوع الأرشيفي فقط**، ولا تمثل بأي شكل من الأشكال مواصفات معمارية سارية، أو إثباتات جاهزية تشغيلية، أو شهادات اعتماد ومطابقة تنظيمية للنواة المحاسبية الحالية (`accounting_engine`). للاطلاع على المواصفات والسياسات السارية، يُرجى الرجوع إلى [فهرس التوثيق الرئيسي](../README.md).

## فهرس الملفات المؤرشفة

| الملف المؤرشف | طبيعته الأصلية | سبب الأرشفة والبديل الساري |
| --- | --- | --- |
| `golden_accounting_benchmark.md` | مسودة بيانات محاسبية معيارية (11 نوفمبر 2026) | استُبدلت بالوثيقة المحاسبية المعتمدة للمهمة P4-08: [`docs/golden_accounting_scenarios.md`](../golden_accounting_scenarios.md). |
| `production_certification_signoff.md` | تقييم سابق للجاهزية والشهادة الإنتاجية | تقرير تاريخي ملغى؛ البديل الساري المعتمد هو تقرير الجاهزية الشامل لإصدار v0.2.0: [`docs/release_readiness_v0.2.0.md`](../release_readiness_v0.2.0.md). |
| `production_readiness_specification_and_verification.md` | مواصفات جاهزية مقترحة سابقة | وثيقة غير معيارية تضمنت مقترحات سابقة؛ البديل الساري هي سياسات التشغيل والفحوصات في [`docs/operations/`](../operations/) وبوابات [`docs/ci.md`](../ci.md). |
| `gap_analysis_and_standards_verification_report.md` | تقرير تدقيق وفحص فجوات (11 نوفمبر 2026) | فحص قديم تم إجراؤه قبل تنفيذ مراحل إعادة التصميم P0–P12. تم استيعاب الفجوات ومعالجتها في الكود و[`docs/release_readiness_v0.2.0.md`](../release_readiness_v0.2.0.md). |
| `reports/release_readiness_p7-05.md` | تقرير الجاهزية المرحلة P7 لـ v0.1.0 (NO-GO) | تقرير مرحلي مؤقت تم تجاوزه ورفع شرطه بالكامل في [`docs/release_readiness_v0.2.0.md`](../release_readiness_v0.2.0.md) بنسبة 100% GO. |
| `ifrs_automation.md` | مسودة أداة CLI مبكرة لمعايير IFRS | استُبدلت بالتكامل البرمجي المباشر عبر الـ SDK Application Layer وموديولات المعايير (`modules/*`) المنجزة في P11-05. |
| `root-facade.md` | إرشادات هجرة مرحلية من Root API إلى `pkg/sdk` | وثيقة مرحلية للمهمة P1-05؛ البديل المعتمد هو [`docs/sdk_developer_guide.md`](../sdk_developer_guide.md). |
| `release_notes/v0.1.0.md` | ملاحظات إصدار v0.1.0 المبدئي | تم دمجها وأرشفتها رسمياً في [`docs/reports/release_archive_v0.1.0.md`](../reports/release_archive_v0.1.0.md) و[`RELEASE_NOTES.md`](../../RELEASE_NOTES.md). |
