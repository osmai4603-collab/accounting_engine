# `ARD-P2-01` — تعريف أوامر ونتائج حالات الاستخدام المحاسبية

- **المهمة:** `P2-01`
- **المرحلة:** `P2`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة

## 1. كيف تم التنفيذ

- تم تثبيت عقود `pkg/spi/application` كواجهة موحدة لحالات الاستخدام المحاسبية:
  - `CreateDraftCommand`
  - `ValidateEntryCommand`
  - `PostEntryCommand`
  - `ReverseEntryCommand`
  - `GetEntryQuery`
  - `EntryResult`
  - `Service`
- تم فصل `Principal` عن `Reason` واحتفظنا بوضوح `Context` للهوية والارتباط والتكرار ومصدر النظام:
  - `Principal.ActorID`
  - `Context.CorrelationID`
  - `Context.IdempotencyKey`
  - `Context.SourceSystem`
- تم توضيح أن `Reason` للعكس مخصص لسياق الأعمال، بينما `ActorID` هو هوية المنفذ المُصدّقة، وبهذا نمنع خلط هوية المرسل مع سبب المعاملة.
- أُضيفت اختبارات حزمة `pkg/spi/application` للتحقق من أن:
  - هوية المنفذ منفصلة عن سبب العكس،
  - Context يحمل بيانات التكرار والارتباط ومصدر النظام،
  - `CreateDraftCommand` يحمل intent كاملًا للحقول الأساسية والسطور والسياق.

## 2. إثبات معايير القبول

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| لكل عملية عامة Command/Query موثق | `pkg/spi/application/application.go` | محقق |
| سبب العكس لا يستخدم كهوية منشئ أو مرحّل | `ReverseEntryCommand.Reason` منفصل عن `Context.Principal.ActorID` | محقق |
| مدخلات النظام المضيف مفصولة عن بيانات المحرك المثبتة | `Principal` و`Context` كعقد منفصلين | محقق |
| اختبارات تحقق للمدخلات المطلوبة والاختيارية والتواريخ والمعرفات | `pkg/spi/application/application_test.go` | محقق |

## 3. المراجع

- `pkg/spi/application/application.go`
- `pkg/spi/application/application_test.go`

## 4. ملاحظات

- هذا التنفيذ يضع الأساس الفعلي لـ `P2-02` المقبل: توحيد Application Layer الفعلي في `Use Case` واحد يطبق هذه العقود عبر الكود، بدلًا من تبعثر الانشاءات في كل طبقة.
