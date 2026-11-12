# 🏛️ وثيقة التقييم المعماري وإعادة التصميم: المحرك المحاسبي الشامل المستقل (Universal Accounting Engine)

> **الحالة:** وثيقة استراتيجية وتصميم معماري ملزم  
> **التاريخ:** 2026-11-12  
> **النطاق:** تحويل `accounting_engine` من مجرد حزمة داخلية مقترنة إلى منتج برمجي ومحرك محاسبي عام قابل للحقن وإعادة الاستخدام في أي نظام تشغيلي (تجاري، صناعي، زراعي، خدمي)  
> **الملف المستهدف:** `docs/universal_accounting_engine_redesign.md`

---

## 1. التقييم الصريح والمباشر: هل المحرك متوافق حالياً مع هذه الرؤية؟

### الإجابة القاطعة: **لا، المحرك في وضعه الراهن غير متوافق مع هذه الرؤية.**

على الرغم من أن المحرك يحتوي على **تنفيذ محاسبي داخلي متين جداً** من حيث نزاهة القيد المزدوج، وقفل الفترات المالية، والتوافق مع تفاصيل المعايير الدولية (IFRS 9, 15, 16, IAS 12)، وحسابات الفروق، إلا أنه من المنظور المعماري كـ **منتج برمجي قابل لإعادة الاستخدام والحقن (Reusable & Injectable Software Product)** يعاني من انسداد هيكلي يمنعه من العمل خارج مستودعه الحالي.

---

### أسباب عدم التوافق (تشريح الخلل المعماري الحالي)

#### 1. خرق قواعد التغليف في Go وجعل الاستيراد الخارجي مستحيلاً (Package Encapsulation Violation)

- جميع النطاقات الأساسية موجودة تحت مجلد `internal/` (`internal/posting`, `internal/masterdata`, `internal/reporting`, `internal/types`, إلخ).
- في `engine.go`، يتم تصدير دوال وحقول تستخدم أنواعاً من حزم `internal`:

  ```go
  // engine.go حالياً:
  type AccountingEngine struct {
      MasterData *masterdata.Service // internal!
      Posting    *posting.Engine    // internal!
      Reporting  *reporting.Engine  // internal!
      ...
  }

  func (e *AccountingEngine) PostStandardsEntryContext(
      ctx context.Context,
      companyID uuid.UUID,
      entry posting.JournalEntry, // internal!
      posterID string,
  ) (posting.JournalEntry, error)
  ```

- **النتيجة الحتمية:** إذا حاول أي مشروع خارجي (تطبيق زراعي أو تجاري في مستودع آخر) استيراد `accounting_engine` واستدعاء `PostStandardsEntryContext` أو الوصول إلى `Posting`، سيرفض مترجم Go ذلك فوراً بخطأ تصريف صارم:
  `use of internal package accounting_engine/internal/... not allowed`. المحرك محبوس ومقفل ذاتياً داخل مستودعه الحالي.

#### 2. تسريب التفاصيل الداخلية للمستهلك (Leaky Abstraction)

- بدلاً من أن يقدم المحرك واجهة تجريدية عليا تمثل "الدفتر العام" أو "خدمة المحاسبة"، يقوم `AccountingEngine` بتصدير المؤشرات المباشرة للخدمات الداخلية كحقول عامة (`e.Posting`, `e.Reporting`, `e.Subledgers`).
- النظام الخارجي يجد نفسه مجبراً على التنقل بين تعقيدات داخلية صُممت لخدمة منطق المحرك الداخلي وليس المستهلك الخارجي.

#### 3. إجبار النظام الخارجي على التفكير كمحاسب وقيد يدوي (Lack of Accounting Event Layer)

- لكي يسجل نظام مبيعات تجاري أو نظام حصاد زراعي عملية ما، يطلب منه المحرك حالياً تجهيز `posting.JournalEntry` كامل مع سطور المدين والدائن (`Debit`, `Credit`, `AccountID`, `UUIDs`).
- هذا يجعل النظام الخارجي بحاجة لمعرفة الحسابات التفصيلية والقواعد المحاسبية، مما يعني أن المنطق المحاسبي تسرب إلى التطبيق الخارجي، وفقد المحرك ميزته كعقل محاسبي مستقل.

#### 4. الربط الكتلي الصارم بجميع المعايير والبنية التحتية (Monolithic Coupling)

- دالة التهيئة `New(driver StorageDriver, config Config)` و `StorageDriver` تفترض وجود سائق تخزين واحد يغطي كل شيء: الترحيل، التقارير، البيانات الأساسية، والـ SPI الخاص بالأسعار، والرقابة.
- المحرك يربط صراحةً وحدات IFRS 9 و 15 و 16 و IAS 12 داخل الـ Struct الرئيسي للـ Engine. إذا كان هناك نظام زراعي بسيط يحتاج فقط لنظام حسابات وقيد مزدوج وتقارير ختامية دون تعقيدات المشتقات المالية وإيجارات IFRS 16، فإنه يُجبر على حمل وتهيئة هذه الكتل.

---

## 2. Universal Accounting Engine مقابل Universal Business Engine

> **قاعدة ذهبية:** الهدف هو بناء **Universal Accounting Engine** وليس **Universal Business Engine**.

إذا حاولت جعل المحرك يفهم ما هي "شحنة القمح"، أو "أصناف التجزئة"، أو "ساعات عمل الاستشاري"، أو "أمر تصنيع خط إنتاج"، فسيتحول المحرك إلى ERP ضخم ومشتت، وستفقد أهم ميزة: **الاستقلالية وإعادة الاستخدام**.

| المقارنة | Universal Business Engine (فخ تضخم الـ ERP) | Universal Accounting Engine (الرؤية الصحيحة) |
| :--- | :--- | :--- |
| **ماذا يعرف؟** | يعرف تفاصيل البضاعة، الأطنان، الموردين، جداول المناوبات، خطوط التجميع. | يعرف فقط الأثر المالي: القيمة، التاريخ، الأطراف (حساب ضابط/فرعي)، العملة، الضرائب، والالتزام المحاسبي. |
| **كيف يستقبل البيانات؟** | نماذج بيانات معقدة خاصة بكل نشاط (AgriculturalBatch, RetailPOSCart). | أحداث اقتصادية مجردة (Economic / Accounting Events) مع سمات تحليلية (Dimensions). |
| **طبيعة الاعتمادية** | يتغير كود المحرك كلما تغير نشاط العمل أو أسلوب التسويق والمبيعات. | ثابت وصامد؛ قواعد المحاسبة لا تتغير بتغير النشاط، بل تتغير القواعد الرابطة (Posting Rules). |
| **إمكانية إعادة الاستخدام** | ضعيفة جداً ومكلفة؛ يحتاج لإعادة برمجة لكل قطاع. | شبه مطلقة؛ نفس المحرك يُحقن في متجر تجزئة أو مزرعة أبقار أو شركة استشارات هندسية. |

### كيف يرى المحرك الأنشطة المختلفة دون أن يتلوث بها؟

يرى المحرك كل نشاط من خلال **ثلاثية التجريد المحاسبي**:

1. **الحدث المالي (Financial Event):** ماذا حدث مالياً؟ (تبادل منفعة، نشوء التزام، تحقق إيراد، استهلاك أصل).
2. **الأبعاد التحليلية (Dimensions):** أين ومع من حدث؟ (مركز تكلفة، مشروع، خط إنتاج، موسم زراعي، فرع تجاري). هذه مجرد Keys و Values للمحرك ولا تهمه طبيعتها الفيزيائية.
3. **قواعد التوجيه والتسجيل (Posting Rules / Accounting Schema):** كيف يُترجم الحدث التشغيلي إلى قيود؟ (مصفوفة تكويد خارج النواة تربط كل نوع حدث بالحسابات المناسبة).

---

## 3. حسم القرارات الثلاثة الاستراتيجية قبل إعادة التصميم

### القرار الأول: طريقة استخدام المحرك (Microservice أم Go Library؟)

#### الخيار المعتمد: **In-Process Go Library (Core Engine) مع قابلية التغليف كـ Standalone Service (Dual-Mode Architecture)**

1. **النواة الصرفة (Pure Library First):**
   - يُبنى المحرك كـ **Go Module مستقل ونقي** يمكن تضمينه عبر `go get` مباشرة في أي تطبيق Go (Monolith أو Microservice).
   - **السبب:** العمليات المحاسبية تتطلب غالباً سرعة فائقة (High Throughput)، وتوافقاً مع المعاملات الذرية لقاعدة بيانات التطبيق المضيف (Single Transactional Boundary)، وصفر Network Latency.
2. **التغليف الخدمي الاختياري (Service Wrapper):**
   - يوفر المشروع مجلداً مستقلاً `cmd/accounting-service` (أو Sidecar) يغلف المكتبة ويعرض واجهات **gRPC** و **HTTP/REST** مع دعم CloudEvents و Prometheus.
   - هذا يتيح لأي نظام مبني بلغة أخرى (Node.js, Python, Java, C#) استخدام المحرك كـ Microservice مستقلة دون المساس بنواة المكتبة.

---

### القرار الثاني: طريقة استقبال العمليات المحاسبية

#### الخيار المعتمد: **واجهة استقبال الأحداث المحاسبية (Accounting Event Ingestion via Posting Rules) + واجهة صريحة للقيود المباشرة**

لا ينبغي للنظام الخارجي أن يبني قيود اليومية يدوياً إذا لم يكن يريد ذلك. نوفر طبقتين للدخول:

1. **الطبقة القياسية العامة (Business / Event Ingestion Layer):**
   - يستقبل المحرك كائناً مجرداً يسمى `AccountingEvent`:

     ```go
     type AccountingEvent struct {
         EventID        string
         EventType      string // e.g., "crop_harvested", "pos_sale", "service_billed"
         CompanyID      string
         EffectiveDate  time.Time
         TransactionAmount Money
         Dimensions     map[string]string // "farm_id": "north_zone", "crop": "wheat"
         Payload        map[string]any
         IdempotencyKey string
     }
     ```

   - يتولى المحرك مطابقة `EventType` مع **مخطط التوجيه المحاسبي (Posting Rule Mapping)** المخصص لتلك الشركة، ويولد تلقائياً قيد اليومية المتزن ويفحصه ويرحله ذرياً.
2. **طبقة القيود المحاسبية الصريحة (Direct Journal Voucher Entry):**
   - مخصصة للتسويات اليدوية، أو لقيود المحاسبين المباشرة، أو للأنظمة التي تملك بالفعل محرك توجيه خاص بها وتريد فقط ضمانات الترحيل والتسلسل والنزاهة من الدفتر العام.

---

### القرار الثالث: طريقة تركيب وتوصيل الوحدات المحاسبية (Modular SPI Architecture)

#### الخيار المعتمد: **نواة خفيفة إلزامية (Micro-Kernel) + وحدات معايير اختيارية (Pluggable Standard Modules)**

- **النواة الإلزامية (Core Ledger):**
  - دليل الحسابات (Chart of Accounts).
  - الفترات المالية والإقفال (Fiscal Periods & Calendars).
  - محرك الترحيل الذري والقيد المزدوج وسلسلة التدقيق (Double-Entry Posting Engine & WORM Hash Chain).
  - العملات وتعدد العملات (Multi-currency Valuation).
  - التقارير الختامية الأساسية (Trial Balance, General Ledger, Balance Sheet, Income Statement).
- **الوحدات الاختيارية كإضافات (Optional Add-on Units):**
  - `IFRS 15` (الاعتراف بالإيراد والعقود).
  - `IFRS 16` (عقود الإيجار وحق الاستخدام).
  - `IFRS 9` (مخصصات التدني ECL والأدوات المالية).
  - `IAS 12` (الضرائب المؤجلة).
  - `Consolidation` (القوائم المالية الموحدة للمجموعات).
  - `Cost Accounting & Allocation` (محاسبة التكاليف المتقدمة والتوزيع).
- يتم تفعيل الوحدات عبر الواجهة البرمجية (Builder Options) فقط إذا كانت مرخصة ومطلوبة، ولا يتم تحميل جداولها أو متطلباتها إذا لم يتم تفعيلها.

---

## 4. تطبيق المبادئ الهندسية السبعة لإعادة التصميم

### المبدأ 1: كل نوع يظهر في Public API يجب أن يكون قابلًا للاستيراد من مشروع خارجي

- تفكيك الاعتماديات على `internal`: يتم إنشاء حزم عامة مفتوحة تحت مجلد `pkg/`:
  - `pkg/api/v1/types/`: يحتوي على الكيانات العامة (`Money`, `Currency`, `CompanyID`, `AccountID`, إلخ).
  - `pkg/api/v1/ledger/`: يحتوي على واجهات وعقود الترحيل والحسابات والتقارير.
  - `pkg/api/v1/events/`: يحتوي على عقود الأحداث المحاسبية.
  - `pkg/api/v1/standards/`: يحتوي على واجهات تفعيل المعايير الاختيارية.
- مجلد `internal/` يقتصر تماماً على الخوارزميات المنطقية والتفاصيل التنفيذية التي لا يحتاجها المستهلك الخارجي.

### المبدأ 2: عدم عرض تفاصيل التنفيذ الداخلية للمستهلك (Encapsulated Facade)

- بدلاً من تصدير `PostingEngine` و `ReportingEngine` الممتلئين بدوال التخزين والتزامن والتسلسل الداخلي، يقدم المحرك واجهة Facade موحدة وقوية:

  ```go
  type Engine interface {
      Ledger() LedgerService
      Reporting() ReportingService
      Governance() GovernanceService
      Events() EventIngestionService
      Close() error
  }
  ```

- كل خدمة تعرض فقط العمليات المطلوبة للأعمال (Use Cases) مع إخفاء كامل لآليات القفل والمزامنة وسلاسل التجزئة.

### المبدأ 3: عدم إلزام النظام الخارجي بمعرفة بنية PostgreSQL الخاصة بالمحرك

- المحرك يمتلك ويدير جداوله الخاصة به حصرياً داخل Namespace أو Schema مخصصة (مثل `accounting_core.*`)، أو يدير مفاتيح الربط بهدوء عبر الـ Migration الذاتي المعزول.
- المستهلك الخارجي لا يكتب استعلام SQL واحداً ضد جداول المحرك، ولا يفترض وجود أعمدة محددة؛ بل يتعامل فقط عبر Go API أو gRPC.
- إذا أراد المستهلك استرجاع ميزان المراجعة أو كشف الحساب، يحصل عليه كـ DTOs جاهزة أو عبر Streams، دون أي استعلام مباشر على `account_moves`.

### المبدأ 4: عدم إلزام النظام الخارجي بمعرفة تفاصيل `posting.Engine`

- `posting.Engine` بمثابة قلب المحرك الداخلي؛ المستهلك لا يحتاج لمعرفة كيف يعمل الـ Draft، أو كيف يتم فحص الـ Hash، أو بصمة الاعتماد (Approval Fingerprint)، أو مصفوفة منع التعارض (SoD).
- المستهلك ينفذ أمراً عالي المستوى:

  ```go
  result, err := engine.Ledger().PostTransaction(ctx, cmd)
  ```

- المحرك داخلياً هو من يتحقق من الفترة المغلقة، ويولد البصمة، ويفحص موافقة الصلاحيات، ويسجل القيد، ويحدث الأرصدة ذرياً.

### المبدأ 5: جعل وحدات المعايير المحاسبية اختيارية تماماً (Pluggable Standards)

- إزالة المعايير من صلب هيكل `AccountingEngine`.
- تتحول المعايير إلى Plugins أو Modules تطبق واجهة عامة:

  ```go
  type AccountingStandardModule interface {
      Name() string
      RegisterRules(router *RuleRouter) error
      RequiredStoragePorts() []string
  }
  ```

- إذا لم يطلب المستهلك وحدة IFRS 16، فلن يتم استهلاك ذاكرة لها، ولن تطلب أي جداول تخزين، ولن تظهر في خيارات التشغيل.

### المبدأ 6: فصل واجهات التكامل (Integration Ports) عن واجهات التخزين (Storage SPIs)

- **واجهات التكامل (Inbound Ports / API):** ما يقدمه المحرك للعالم الخارجي (استقبال حدث، استخراج تقرير، اعتماد قيد، إقفال فترة).
- **واجهات التخزين (Outbound SPI / Storage Ports):** ما يحتاجه المحرك من البنية التحتية لحفظ واسترجاع بياناته (حفظ قيد، تحديث رصيد، قراءة سعر صرف).
- **عزل التخزين لكل مجال:** فصل `LedgerStoragePort` عن `ReportingStoragePort` عن `AuditStoragePort`؛ مما يسمح مثلاً بوضع سجل التدقيق على S3/WORM والتخزين الأساسي على PostgreSQL وقراءة التقارير من Read-Replica دون إجبار السائق على جمعهم قسراً.

### المبدأ 7: توفير نسخ وإصدارات واضحة للعقود العامة (Versioning & Semantic Contracts)

- اعتماد هيكل الإصدارات في الحزم العامة:
  `pkg/api/v1` للواجهات الأساسية المستقرة.
- أي تعديل في العقود العامة يخضع لقواعد التوافق العكسي الصارم (Semantic Versioning)، وتستخدم الـ Builders والـ DTOs مع قنوات عزل تمنع كسر المشاريع الخارجية التي تعتمد على المحرك.

---

## 5. إعادة تصميم Bootstrap: التفعيل المشروط وشجرة الاعتماديات

### مشكلة الـ Bootstrap الحالي

في التصميم الحالي:

- إما أن تمرر `StorageDriver` ينفذ كل الواجهات معاً، أو تمرر `StoragePorts` التي تشترط مسبقاً 6 منافذ دفعة واحدة، دون فحص ذكي لما إذا كانت الميزات المطلوبة تستلزمها فعلاً أم لا.

### التصميم الجديد المقترح (Dependency-Driven Builder Pattern)

```go
package accounting

// Builder يدير شجرة الاعتماديات ويتحقق من تلبية كل متطلبات الميزات في وقت التهيئة
type Builder struct {
    storagePorts StoragePorts
    enabledFeatures featureSet
    standards       []StandardModule
    observability   ObservabilityOptions
    errs            []error
}

func NewBuilder() *Builder {
    return &Builder{}
}

// توفير مخزن النواة المحاسبية (إلزامي للترحيل)
func (b *Builder) WithCoreStorage(store CoreLedgerStore) *Builder {
    b.storagePorts.Core = store
    return b
}

// تفعيل ميزة التقارير - يشترط وجود ReportingStore
func (b *Builder) WithReporting(store ReportingStore) *Builder {
    if store == nil {
        b.errs = append(b.errs, errors.New("bootstrap: reporting feature requires a non-nil ReportingStore"))
        return b
    }
    b.storagePorts.Reporting = store
    b.enabledFeatures.reporting = true
    return b
}

// تفعيل معيار اختياري - يفحص اعتمادياته الخاصة
func (b *Builder) WithStandard(mod StandardModule) *Builder {
    if mod == nil {
        return b
    }
    b.standards = append(b.standards, mod)
    return b
}

// Build يقوم بالتحقق الشامل من شجرة الاعتماديات
func (b *Builder) Build(ctx context.Context) (Engine, error) {
    if len(b.errs) > 0 {
        return nil, errors.Join(b.errs...)
    }
    if b.storagePorts.Core == nil {
        return nil, errors.New("bootstrap: core ledger storage is strictly required")
    }

    // تحقق من اعتماديات المعايير
    for _, mod := range b.standards {
        if err := mod.ValidateDependencies(b.storagePorts); err != nil {
            return nil, fmt.Errorf("bootstrap: standard module %s unsatisfied: %w", mod.Name(), err)
        }
    }

    return initializeEngineInstance(b)
}
```

**مميزات هذا النموذج:**

1. استحالة تشغيل المحرك في حالة غير متسقة (Fail-fast at construction).
2. عدم الحاجة لتوفير مخازن للميزات غير المستخدمة.
3. رسائل خطأ واضحة ومحددة تصف ما ينقص النظام بالضبط ليعمل.

---

## 6. الاختبارات المعمارية الثلاثة الحاسمة (The 3 Architectural Acid Tests)

هذه الاختبارات لا تختبر هل `1 + 1 = 2` في القيد المزدوج، بل تختبر **هل التصميم المعماري يحقق استقلالية المنتج وقابليته لإعادة الاستخدام في العالم الحقيقي**.

```text
                   ┌─────────────────────────────────────────┐
                   │    Test 1: External Module Consumption   │
                   │  Independent Repo, go get, Zero internal│
                   └────────────────────┬────────────────────┘
                                        │
                                        ▼
                   ┌─────────────────────────────────────────┐
                   │     Test 2: Industry Independence       │
                   │  Same Engine: Retail + Farm + SaaS Apps │
                   └────────────────────┬────────────────────┘
                                        │
                                        ▼
                   ┌─────────────────────────────────────────┐
                   │   Test 3: Infrastructure Independence   │
                   │  Zero PostgreSQL in Core -> Memory/Spanner│
                   └─────────────────────────────────────────┘
```

---

### الاختبار الأول: External Module Consumption (اختبار الاستهلاك من مشروع خارجي نقي)

#### الهدف

إثبات أن المحرك منتج برمجي ومكتبة Go مستقلة يمكن لأي مطور في أي مكان استيرادها في مشروع جديد وبناء نظام مالي متكامل دون الوصول لأي سطر داخل `internal/`.

#### بروتوكول الاختبار

1. إنشاء مستودع تجريبي معزول تماماً خارج مجلد المشروع الحالي:
   `mkdir /tmp/retail_app && cd /tmp/retail_app && go mod init retail_app`
2. إضافة المحرك كاعتمادية:
   `go get github.com/.../accounting_engine`
3. كتابة تطبيق `main.go` يستورد فقط:

   ```go
   import (
       "github.com/.../accounting_engine/pkg/api/v1/ledger"
       "github.com/.../accounting_engine/pkg/api/v1/types"
       "github.com/.../accounting_engine/pkg/sdk"
       "github.com/.../accounting_engine/storage/memory"
   )
   ```

4. إنشاء شركة، وتهيئة دليل حسابات مبسط، وإنشاء فترة مالية، وترحيل قيد مبيعات، واستخراج ميزان المراجعة.

#### معيار النجاح الصارم (Passing Criteria)

- يكتمل `go build` و `go run` بنجاح بنسبة 100%.
- عدم وجود أي إشارة أو استيراد لحزمة داخلية `internal/...`.
- الكود المكتوب نظيف وواضح ويعكس عمليات محاسبية حقيقية دون التعاطي مع كائنات المحرك الداخلية المعقدة.

---

### الاختبار الثاني: Industry Independence (اختبار الاستقلالية عن الأنشطة الاقتصادية)

#### الهدف

إثبات أن المحرك لا يعرف شيئاً عن الزراعة أو الصناعة أو التجارة، ولكنه قادر على خدمة جميع هذه القطاعات بنفس الكفاءة بمجرد ربطه بقواعد التوجيه (Posting Rules) الخاصة بكل نشاط.

#### بروتوكول الاختبار

إنشاء 3 سيناريوهات اختبارية متزامنة تعمل ضد نفس نسخة المحرك المحاسبي:

1. **سيناريو النشاط الزراعي (Agriculture Domain):**
   - **الحدث:** حصاد محصول قمح بتكلفة تشغيلية معينة ونقله للمستودع.
   - **المدخلات للمحرك:** حدث `CropHarvestedEvent` مع أبعاد: `Season: 2026`, `FieldID: F-12`, `Crop: Wheat`.
   - **القيد الناتج عبر قواعد المحرك:**
     - مدين: مخزون الإنتاج الزراعي (Biological/Agricultural Asset - IAS 41).
     - دائن: تكاليف زراعية مجمعة / أجور وعمليات تشغيلية.

2. **سيناريو متجر التجزئة (Retail & E-commerce Domain):**
   - **الحدث:** بيع نقطة بيع (POS Sale) مع ضريبة قيمة مضافة وسداد بالبطاقة.
   - **المدخلات للمحرك:** حدث `POSReceiptEvent` مع أبعاد: `StoreID: Riyadh-01`, `Register: POS-3`.
   - **القيد الناتج عبر قواعد المحرك:**
     - مدين: وسيط سداد بطاقات مدى / بنك.
     - دائن: إيراد مبيعات التجزئة.
     - دائن: ضريبة القيمة المضافة المستحقة.

3. **سيناريو شركة الخدمات البرمجية (SaaS / Services Domain):**
   - **الحدث:** اشتراك سنوي لعميل مدفوع مقدماً مع التزام خدمة (IFRS 15).
   - **المدخلات للمحرك:** حدث `SubscriptionActivatedEvent` مع مدة العقد 12 شهراً.
   - **القيد الناتج عبر قواعد المحرك:**
     - مدين: البنك / نقدية.
     - دائن: إيرادات مؤجلة (Contract Liability / Deferred Revenue).

#### معيار النجاح الصارم (Passing Criteria)

- لم يتم تعديل سطر واحد أو إضافة حقل واحد يخص القمح أو نقاط البيع أو الاشتراكات داخل كود المحرك المحاسبي.
- كل قطاع استطاع ترجمة واقعه الاقتصادي إلى قيود سليمة واستخراج قوائم مالية متوافقة مع متطلبات مجاله عبر الأبعاد التحليلية (Analytical Dimensions).

---

### الاختبار الثالث: Infrastructure Independence (اختبار الاستقلالية عن البنية التحتية والتخزين)

#### الهدف

إثبات أن المحرك لا يعتمد عضوياً على محرك PostgreSQL حصراً، وأن النواة الحسابية خالية من أي استعلام SQL أو تفاصيل تتعلق بقواعد البيانات.

#### بروتوكول الاختبار

1. **تشغيل المحرك في بيئة اختبارات الذاكرة الصرفة (In-Memory Testing):**
   - تشغيل دورة محاسبية كاملة (تسجيل 10,000 قيد متزامن، إقفال شهر، توليد ميزان مراجعة وميزانية عمومية) باستخدام سائق In-Memory Storage فقط.
   - التحقق من أن جميع الاختبارات تعمل بدون الحاجة لتشغيل Docker أو وجود اتصال بشبكة أو قاعدة بيانات.
2. **محاكاة استبدال قاعدة البيانات بمحرك آخر (Pluggable SPI Test):**
   - كتابة Mock/Alternative Adapter بسيط لقاعدة بيانات مختلفة (مثل SQLite أو كائن تخزين تجريدي Key-Value Store) ينفذ فقط واجهات `CoreLedgerStore`.
   - تشغيل المحرك والتحقق من أنه يعمل وينتج نفس القيود وسلاسل التجزئة دون أي اعتراض.

#### معيار النجاح الصارم (Passing Criteria)

- انعدام وجود أي كود SQL أو تعليمات خاصة بـ Postgres داخل `pkg/` أو داخل المنطق الحسابي لنواة الترحيل.
- نجاح اختبارات النزاهة المحاسبية (Zero-drift, Double-entry balance, WORM chain) على أي وسيط تخزين يطبق العقود المحددة.

---

## 7. الهيكل المستهدف للمشروع بعد إعادة التصميم (Target Directory Structure)

```text
accounting_engine/
├── pkg/                                # الواجهات والعقود العامة المتاحة للعالم الخارجي
│   ├── api/
│   │   └── v1/
│   │       ├── types/                  # الأنواع الأساسية (Money, Currency, CompanyID)
│   │       ├── ledger/                 # عقود الترحيل والحسابات والقيود
│   │       ├── events/                 # عقود استقبال الأحداث الاقتصادية وقواعد التوجيه
│   │       └── reporting/              # عقود التقارير والقوائم المالية والـ DTOs
│   └── sdk/                            # نقطة الدخول والمصنع (Bootstrap Builder & Engine Facade)
│       ├── builder.go                  # منشئ المحرك مع إدارة شجرة الاعتماديات
│       ├── engine.go                   # واجهة المحرك العامة (Engine Interface)
│       └── options.go                  # خيارات التكوين
│
├── internal/                           # التفاصيل التنفيذية المحمية تماماً داخل المحرك
│   ├── core/
│   │   ├── ledger/                     # نواة دفتر الأستاذ والتحقق المزدوج
│   │   ├── posting/                    # محرك الترحيل الذري وتوليد سلاسل التدقيق
│   │   ├── period/                     # آلة حالات الفترات والإقفال المالي
│   │   └── valuation/                  # محرك تقييم العملات وفروق الصرف
│   ├── events/                         # محرك معالجة ومطابقة الأحداث بقواعد التوجيه
│   └── reporting/                      # خوارزميات إعداد التقارير المالية وميزان المراجعة
│
├── modules/                            # وحدات المعايير المحاسبية الاختيارية (Pluggable Modules)
│   ├── ifrs15/                         # وحدة عقود العملاء والاعتراف بالإيراد
│   ├── ifrs16/                         # وحدة عقود الإيجار
│   ├── ifrs9/                          # وحدة مخصصات التدني والأدوات المالية
│   └── ias12/                          # وحدة ضرائب الدخل المؤجلة
│
├── storage/                            # محولات التخزين الخارجية (Adapters / SPI Implementations)
│   ├── memory/                         # محول الذاكرة للاختبارات والتطوير السريع
│   └── postgres/                       # محول PostgreSQL عالي الأداء مع الهجرات الخاصة به
│
├── cmd/                                # البرامج القابلة للتشغيل المستقل
│   └── accounting-service/             # خادم gRPC / REST لتشغيل المحرك كـ Microservice مستقل
│
└── docs/                               # التوثيق المعماري والمعايير
    └── universal_accounting_engine_redesign.md
```

---

## 8. خارطة الطريق للتنفيذ (Refactoring Roadmap)

1. **المرحلة الأولى: تحرير العقود العامة (Contracts Extraction & Decoupling)**
   - إنشاء حزم `pkg/api/v1/...` ونقل التعريفات الأساسية (`Money`, `Currency`, `JournalEntry` كعقد عام، `Report` DTOs) خارج `internal/`.
   - ضمان عدم استيراد أي ملف في `pkg/` لأي حزمة داخل `internal/`.

2. **المرحلة الثانية: بناء واجهة المحرك الموحدة و الـ Builder الجديد**
   - إنشاء `pkg/sdk/builder.go` و `pkg/sdk/engine.go`.
   - تطبيق شجرة التحقق من الاعتماديات الصارمة (Dependency-driven Bootstrap).
   - إخفاء كائنات `internal/posting.Engine` و `internal/reporting.Engine` خلف واجهات خدمة نقية.

3. **المرحلة الثالثة: طبقة الأحداث وقواعد التوجيه (Accounting Events & Rules Layer)**
   - بناء مكون `EventIngestionService` يتيح استقبال العمليات التجارية والزراعية والخدمية كأحداث عامة وترجمتها لقيود دون تلويث النواة.

4. **المرحلة الرابعة: عزل المعايير المحاسبية كوحدات مستقلة (Pluggable Modules)**
   - تحويل وحدات IFRS 9, 15, 16, IAS 12 إلى حزم مستقلة تحت `modules/` تطبق واجهة `StandardModule`.

5. **المرحلة الخامسة: تنفيذ الاختبارات المعمارية الثلاثة واعتماد الإصدار v1.0.0**
   - تنفيذ اختبار المشروع الخارجي (`External Module Consumption Test`).
   - تنفيذ اختبار الأنشطة الثلاثة (`Industry Independence Test`).
   - تنفيذ اختبار استبدال التخزين (`Infrastructure Independence Test`).
   - إصدار التوثيق النهائي ووسم النسخة كأول إصدار إنتاجي مستقل وشامل للمحرك.

---

> 🎯 **الخلاصة:**  
> بتحويل المحرك من وضعه الراهن إلى هذا التصميم، يتحقق هدفك الأساسي بنسبة 100%: **محرك محاسبي عالمي حقيقي، نقي، مستقل، يمكن حقنه في أي مشروع تجاري أو صناعي أو زراعي أو خدمي، ليتولى الجانب المالي المحاسبي بالكامل بضمانات عالمية ودون أن يفرض على الأنظمة المضيفة قيوداً أو تفاصيل تقنية تعيق تطورها.**
