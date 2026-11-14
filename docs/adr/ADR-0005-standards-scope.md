# ADR-0005 — نطاق الوحدات المعيارية وحدود الادعاء

- **الحالة:** معتمد
- **التاريخ:** 2026-11-14
- **يخدم:** `P0-04`, `P5-04` إلى `P5-09`

## السياق

`modules/ifrs15`, `ifrs16`, `ifrs9`, `ias12` كانت تسجل قواعد ثابتة (`RegisterRules`) تربط حسابين، بينما الاسم يوحي بتنفيذ معيار كامل. لا يجوز أن يستنتج المستخدم الامتثال من اسم الحزمة أو من نجاح اختبار تقني.

## البدائل

1. إعلان الامتثال الكامل: غير مثبت ومرفوض.
2. إزالة الوحدات: يفقد المنتج قدرات اختيارية مطلوبة.
3. Capabilities اختيارية بنطاق معلن وCompliance Matrix. (المختار)

## القرار

1. كل وحدة تصرّح بـ: Standard, Edition/Amendments, Effective Date, Jurisdiction, Supported Requirements, Exclusions, Dependencies, Version، وتعرض Workflows محدودة ومدخلاتها ومخرجاتها.
2. لا تُنتج الوحدة قيدًا إلا عبر Application Layer وJournal Mapping حقيقي؛ لا UUID اصطناعي.
3. `RegisterRules` وحدها لا تكفي: يلزم Workflow/Use Case يحدد البيانات والقياس والتحقق والمخرجات.
4. كل Requirement في Compliance Matrix له Implementation Reference وTest Evidence أو حالة `Not Implemented`.
5. التفعيل مشروط بـ Capability Preflight: غياب Master Data/Valuation/Reporting/Journal Mapping يفشل البناء قبل الترحيل. لا تسجل الوحدات قواعد حسابات ثابتة؛ لا ينتج قيد إلا من Workflow يمر عبر عقد التطبيق العام وMapping دفتر يومية حقيقي.
6. تحديد الإصدار الأول:
   - IFRS 15 / 16 / 9 / IAS 12 تُعلن `Limited scope` مع قائمة صريحة بما هو خارج النطاق.
   - لا تُعلن أي وحدة "متوافقة مع IFRS" بشكل عام.
7. أي متطلب غير محسوم يعاد إلى النص الرسمي الساري ومراجعة محاسب مؤهل قبل التنفيذ.

## النتائج

- `docs/compliance_matrix.md` (أو تحديث `ENGINE_COMPLIANCE_MATRIX.md`) يصبح مرجع الحالة.
- وحدات P5 تُفعّل فقط عند توفر اعتمادياتها.
- Golden Scenarios إلزامية قبل إعلان أي نطاق.

## معايير التحقق

- لكل وحدة Version/Scope/Exclusions في الكود والتوثيق.
- اختبار يثبت فشل التفعيل عند غياب Journal Mapping.
- لا يوجد نص توثيقي يدّعي امتثالًا أوسع من المصفوفة.
