# 🛠️ منهجية عملية لبناء أي محرك

هذه الوثيقة مكملة لـ [الدليل الهندسي لبناء المحركات البرمجية](./software_engines.md). تعرض منهجية تجمع بين هندسة المتطلبات والمعمارية والتطوير والاختبار.

## 🔄 دورة حياة تطوير المحرك

1. **Problem Definition**

   تحديد المشكلة التي سيعالجها المحرك، ولماذا نحتاج إليه.
2. **Domain Analysis**

   فهم المجال وقواعده ومفاهيمه والعمليات التي يحتوي عليها.
3. **Requirements Engineering**

   تحديد المتطلبات الوظيفية وغير الوظيفية.
4. **Architecture Design**

   تحديد حدود المحرك والمكونات والعلاقات والاعتماديات.
5. **Core Design**

   تصميم النواة والقواعد والمحركات الفرعية والواجهات.
6. **Implementation**

   تنفيذ المكونات تدريجياً.
7. **Verification & Validation**

   اختبار صحة التنفيذ ومطابقته للمتطلبات.
8. **Benchmarking**

   قياس الأداء والجودة ومقارنة النتائج بالمتطلبات.
9. **Release & Operations**

   إصدار المحرك ومراقبته وإدارة إصداراته.
10. **Continuous Improvement**

    معالجة العيوب وتطوير القدرات بناءً على النتائج.

## 🔎 المرحلة الأولى: Problem Definition

قبل كتابة أي كود، أجب عن هذه الأسئلة:

1. ما المشكلة التي يحلها المحرك؟
2. من سيستخدمه؟
3. ما العمليات التي سينفذها؟
4. ما العمليات التي لن يكون مسؤولاً عنها؟
5. ما الذي يميزه عن مجرد مجموعة من الدوال؟
6. هل توجد حلول جاهزة يمكن استخدامها بدلاً من بنائه؟

> 📦 **المخرج:** وثيقة Problem Statement تحدد المشكلة والقيمة المتوقعة من المحرك.

## 🧠 المرحلة الثانية: Domain Analysis

هذه مرحلة شديدة الأهمية، خصوصاً في المحركات المتخصصة.

يجب أن تحدد:

- Domain Entities.
- Value Objects.
- Domain Events.
- Business Rules.
- Invariants.
- State Transitions.
- Domain Services.
- Domain Boundaries.

مثلاً في Accounting Engine:

- Account.
- Journal.
- Journal Entry.
- Ledger.
- Fiscal Period.
- Posting.
- Accounting Rule.

لا تبدأ بتصميم جداول قاعدة البيانات قبل فهم هذه المفاهيم والعلاقات بينها.

> 📦 **المخرج:** Domain Model + Business Rules Catalog.

## 📝 المرحلة الثالثة: Requirements Engineering

قسّم المتطلبات إلى نوعين:

**✅ Functional Requirements**

ما الذي يجب أن يفعله المحرك؟

مثلاً:

- إنشاء عملية.
- التحقق من صحتها.
- تنفيذ العملية.
- إرجاع النتيجة.
- تسجيل الأحداث المطلوبة.

**⚙️ Non-Functional Requirements**

ما الصفات التي يجب أن يتمتع بها؟

مثلاً:

- Performance.
- Reliability.
- Security.
- Scalability.
- Maintainability.
- Compatibility.

يجب أن تكون المتطلبات قابلة للتحقق، وليست مجرد أوصاف عامة.

> 📦 **المخرج:** Software Requirements Specification (SRS)، مع ربط كل متطلب بطريقة اختباره.

## 🏛️ المرحلة الرابعة: Architecture Design

الآن نحدد الهيكل المعماري.

أقترح البدء بفصل ثلاثة مستويات مفاهيمية:

```text
Application / Delivery Layer
HTTP · CLI · gRPC · SDK
             │
             ▼
Engine Core
Domain Rules · Processing · Calculations · Validation
             │
             ▼
Infrastructure Adapters
Database · Cache · Message Broker · External Systems
```

هذا رسم مفاهيمي وليس مخطط اعتماديات نهائياً؛ في تصميم Hexagonal Architecture مثلاً، تكون المنافذ (Ports) ضمن حدود النواة أو التطبيق، وتنفذ المحولات (Adapters) تلك المنافذ.

يجب أن تحدد أيضاً:

- ما الذي يمتلكه المحرك؟
- ما الذي يحق له استدعاؤه؟
- ما الذي لا ينبغي أن يعرفه؟
- كيف يتعامل مع البيانات الخارجية؟
- كيف تتم إدارة الأخطاء؟
- كيف تتم إدارة الحالة والمعاملات؟

> 📦 **المخرج:** Architecture Description + Component Diagram + Dependency Rules.

## 🧩 المرحلة الخامسة: Core Design

هذه المرحلة هي قلب المشروع.

صمم الواجهات والعقود قبل التوسع في التنفيذ.

مثلاً:

```text
type Engine interface {
    Execute(
        ctx context.Context,
        command Command,
    ) (Result, error)
}
```

هذه واجهة توضيحية فقط؛ يجب تخصيصها حسب طبيعة المحرك.

حدد أيضاً:

- Input Contracts.
- Output Contracts.
- Error Contracts.
- Validation Contracts.
- Execution Lifecycle.
- Transaction Boundaries.
- Concurrency Model.
- Extension Points.

من المهم ألا تجعل كل محرك يحتاج إلى واجهة واحدة عامة مثل `Execute` إذا كانت العمليات مختلفة جذرياً؛ يمكن أن تكون هناك واجهات متخصصة.

## 🛠️ المرحلة السادسة: Implementation

ابدأ بتنفيذ أصغر مسار وظيفي كامل (Vertical Slice).

مثلاً:

1. استقبال أمر.
2. التحقق من المدخلات.
3. تطبيق قاعدة المجال.
4. تنفيذ العملية.
5. إنتاج النتيجة.
6. اختبار المسار بالكامل.

ثم أضف القدرات تدريجياً.

لا تبدأ ببناء عشرات المحركات الفرعية قبل التأكد من أن النواة تؤدي وظيفتها الأساسية.

## 🧪 المرحلة السابعة: Verification & Validation

هناك فرق مهم:

- Verification: هل بنينا المحرك بصورة صحيحة وفق المواصفات؟
- Validation: هل بنينا المحرك الذي يحتاجه المستخدم فعلاً؟

اختبارات المحرك ينبغي أن تشمل:

| الاختبار               | الهدف                               |
| ---------------------- | ----------------------------------- |
| Unit Testing           | اختبار القواعد منفردة               |
| Integration Testing    | اختبار تكامل المكونات               |
| Contract Testing       | التأكد من صحة الواجهات              |
| Property-based Testing | اختبار خصائص عامة على مدخلات متنوعة |
| Fuzz Testing           | اكتشاف سلوكيات غير متوقعة           |
| Regression Testing     | منع عودة الأخطاء السابقة            |
| Load Testing           | قياس الأداء تحت الضغط               |
| Fault Injection        | اختبار التعامل مع الأعطال           |

## 📈 المرحلة الثامنة: Benchmarking

لا تعتمد على الانطباعات الشخصية.

أنشئ Benchmark Suite تقيس:

- زمن تنفيذ كل عملية.
- حجم الذاكرة.
- عدد العمليات في الثانية.
- الأداء عند أحجام بيانات مختلفة.
- الأداء عند التزامن.
- تدهور الأداء عند حدوث أخطاء.

يجب توثيق بيئة الاختبار، ونوع البيانات، وحجم الحمل، وإصدار المحرك؛ حتى تكون المقارنة عادلة وقابلة للتكرار.

## 🚀 المرحلة التاسعة: Release & Operations

حتى المحرك المكتمل وظيفياً يحتاج إلى إدارة إصدار وتشغيل.

يجب أن تحدد:

- Versioning Policy.
- Backward Compatibility.
- Migration Strategy.
- Configuration Management.
- Logging and Monitoring.
- Release Notes.
- Rollback Strategy.
- Security Updates.
