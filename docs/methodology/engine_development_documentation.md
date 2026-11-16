# 📄 الوثائق المطلوبة أثناء بناء المحرك

هذا الملف مكمل لـ [الدليل الهندسي لبناء المحركات البرمجية](./software_engines.md)، ويجمع الوثائق المقترح إعدادها لتحديد ما الذي يبنيه الفريق.

من الأخطاء الشائعة أن يبدأ المطور بالبرمجة دون وجود وثائق تحدد ما الذي يبنيه. أقترح اعتماد هذه الوثائق:

| الوثيقة                      | الغرض                    |
| ---------------------------- | ------------------------ |
| Problem Statement            | تعريف المشكلة            |
| SRS                          | تحديد المتطلبات          |
| Domain Model                 | وصف مفاهيم المجال        |
| Business Rules Catalog       | توثيق القواعد            |
| Architecture Description     | توثيق المعمارية          |
| ADR                          | توثيق القرارات المعمارية |
| Component Diagram            | توضيح المكونات           |
| API / Contract Specification | توثيق العقود             |
| Data Model                   | وصف البيانات             |
| Test Strategy                | استراتيجية الاختبارات    |
| Quality Measurement Plan     | خطة قياس الجودة          |
| Security Requirements        | المتطلبات الأمنية        |
| Operations Manual            | دليل التشغيل والصيانة    |

من المهم دراسة ADR تحديداً (Architecture Decision Records)، لأنها تحفظ سبب اتخاذ القرار، وليس القرار وحده.

مثلاً:

- لماذا اخترنا PostgreSQL؟
- لماذا اخترنا Modular Monolith بدلاً من Microservices؟
- لماذا وضعنا Posting داخل Accounting Engine وليس داخل Application Layer؟

هذه الأسئلة يجب أن يكون لها توثيق واضح.
