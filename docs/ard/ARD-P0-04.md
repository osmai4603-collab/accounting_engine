# `ARD-P0-04` — اعتماد قرارات التصميم وتجميد النطاق

- **المهمة:** `P0-04`
- **المرحلة:** `P0`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة

## 1. كيف تم التنفيذ

- الالتزام بحدود التوازي: قرارات وثائقية فقط؛ لا تعديل كود.
- حُسمت القرارات مع مالك المشروع في 2026-11-14 وسُجلت في ADRs منفصلة:
  1. `docs/adr/ADR-0001-public-entry-point.md`: `pkg/sdk` نقطة الدخول الوحيدة؛ Root Facade مهمَل بلا أنواع داخلية.
  2. `docs/adr/ADR-0002-module-path-semver.md`: المسار `github.com/osmai4603-collab/accounting_engine` وسياسة SemVer.
  3. `docs/adr/ADR-0003-application-layer-uow.md`: Application Layer واحدة + Ports + Unit of Work.
  4. `docs/adr/ADR-0004-auth-and-company-context.md`: المضيف يملك الهوية؛ المحرك يتحقق من Principal وعضوية الشركة؛ RLS دفاعي.
  5. `docs/adr/ADR-0005-standards-scope.md`: نطاق محدود معلن لكل معيار + Compliance Matrix.
  6. `docs/adr/ADR-0006-release-form-storage-toolchain.md`: Library Embedded SDK؛ Memory للاختبار؛ Toolchain مجمّد؛ تجميد التوسع.
- تحديث `docs/accounting_engine_impl_plan.md` بقسم «قرارات المالك» وملاحظة أثر قرار شكل الإصدار على P6.

## 2. إثبات معايير القبول

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| ADR لكل قرار أو توثيق سبب دمج بعضها | 6 ملفات ADR مستقلة في `docs/adr/` | محقق |
| لا توجد قرارات أساسية معلقة تؤثر على Public API أو Storage SPI | Module path، Root facade، Application/UoW، Auth، Standards، Release form كلها محسومة | محقق |
| كل ADR يضم السياق والبدائل والقرار والنتائج | كل ملف يضم هذه الأقسام | محقق |
| تجميد التوسع موثق وموافق عليه | ADR-0006 قسم 4 + `docs/product_boundary.md` قسم 4 | محقق |

## 3. النواقص

- لا يوجد توقيع رسمي منفصل من المالك على كل ADR؛ الموافقة موثقة في محادثة التنفيذ بتاريخ 2026-11-14. المطلوب لاحقًا توقيع Go/No-Go في P7-05.

## 4. ملاحظات

- قرار «Library فقط للإصدار الأول» يعني تأجيل `P6-04` و`P6-05` (gRPC/HTTP hardening) خارج الإصدار الأول، مع بقاء `P6-01`..`P6-03` و`P6-06` كضوابط لأي نشر خدمي اختياري. هذا تغيير نطاق قرره المالك ويجب أن ينعكس في Release Notes.
