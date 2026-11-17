# ARD-P8-01: ترحيل واجهة StandardModule من `legacy.CoreLedgerStore` إلى `ModulePorts`

- **المهمة:** `P8-01`
- **المرحلة:** `P8`
- **تاريخ الإتمام:** 2026-11-17
- **حالة المهمة:** منفذة.
- **الاعتماديات:** `P7` (مكتملة).
- **الأدلة الرئيسية:**
  - عقد المنافذ العامة: [`pkg/spi/standards/ports.go`](../../pkg/spi/standards/ports.go)
  - عقد الوحدة العامة: [`pkg/spi/standards/module.go`](../../pkg/spi/standards/module.go)
  - محولات المسار القديم: [`pkg/sdk/module_ports.go`](../../pkg/sdk/module_ports.go)
  - اختبار العقد والتكافؤ: [`test/standards/module_ports_parity_test.go`](../../test/standards/module_ports_parity_test.go)
  - اختبار نقاء الواجهة: [`pkg/spi/standards/imports_test.go`](../../pkg/spi/standards/imports_test.go)

---

## 1. السياق والقرارات المعمارية (Context & Architectural Decisions)

1. **فصل واجهة الوحدة عن التخزين الملموس:**
   - كانت `StandardModule` تستقبل `legacy.CoreLedgerStore` مباشرة، ما جعل `WithStoragePorts` + `WithStandard` مزيجاً مرفوضاً في الـ Builder.
   - تم استبدال ذلك بعقد `standards.ModulePorts` الذي يكشف أربع قدرات فقط:
     - `Entries() EntryReader` — قراءة القيود.
     - `Balance() BalanceReader` — قراءة الأرصدة (اختياري).
     - `MasterData() MasterDataReader` — قراءة الشركات والحسابات والدفاتر.
     - `Application() publicapplication.Service` — تقديم المسودات والترحيل عبر طبقة التطبيق.

2. **منع تسرب الصلاحيات:**
   - `ModulePorts` أضيق من `storage.Ports` عمداً: لا يمنح الوحدة تخصيص تسلسل، ولا كتابة أرصدة، ولا كتابة مباشرة إلى التخزين.
   - كل قيد يجب أن يمر عبر `Application()` لتبقى قواعد التحقق والصلاحيات وعدم التكرار سارية.

3. **قيم `nil` تعني قدرة غير متاحة:**
   - `Entries` و`Balance` و`MasterData` قد تكون `nil` عندما لا يوفّرها المضيف.
   - `ValidateStandardModulePorts` يفرض فقط البنية الدنيا المشتركة: `Application` و`MasterData` و`Entries`؛ أما `Balance` فاختياري.
   - كل وحدة معايير تتحقق من احتياجاتها الخاصة داخل `ValidateDependencies(ModulePorts)` وتفشل صراحةً عند غياب منفذ تحتاجه.

4. **دعم المسار القديم دون تسريب `legacy`:**
   - `pkg/sdk/module_ports.go` يوفّر محولات داخلية (`legacyModuleEntryReader`, `legacyModuleMasterDataReader`) تحوّل `CoreLedgerStore` إلى `ModulePorts` دون أن تستورد `pkg/spi/standards` أي حزمة `legacy` أو `internal`.
   - عند غياب قدرة في العقد القديم (مثل `Balance` أو `GetJournalByCode`) يفشل الاستدعاء بـ `types.ErrCapabilityNotImplemented` بدلاً من اختلاق سلوك.

5. **توقيت التحقق:**
   - أصبح التحقق من الوحدات (`validateStandardModules`) يجري بعد بناء طبقة التطبيق في `Build`، لأن `ModulePorts` تحتاج `Application()`.
   - يبقى التحقق قبل `Initialize` وقبل `RegisterRules`، فلا تُهيَّأ أي وحدة عند فشل الاعتماديات.

6. **إزالة حظر Ports + Standards:**
   - حُذف رفض `standard modules require WithCoreStorage in this release`.
   - أصبح المسار الحديث يدعم الوحدات عبر `storage.Ports` مباشرة، مع إبقاء المسار القديم عاملاً حتى إزالته المخططة.

---

## 2. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| `WithStoragePorts(ports)` + `WithStandard(module)` يبني المحرك بنجاح | `TestBuilder_PortsModeSupportsStandards` في `pkg/sdk/builder_test.go` | **محقق** |
| `StandardModule` لا تستورد `legacy` ولا `internal` | `TestSPIHasNoLegacyOrInternalImports` في `pkg/spi/standards/imports_test.go` يفحص شجرة الاستيراد الفعلية للحزمة | **محقق** |
| استمرار عمل `WithCoreStorage` + `WithStandard` | `TestBuilder_LegacyPathProvidesModulePorts` واختبارات الوحدات الأربع | **محقق** |
| اجتياز الوحدات الأربع لاختبارات العقود على كلا المسارين | `TestStandardModules_SameContractOnBothStoragePaths` في `test/standards/module_ports_parity_test.go` | **محقق** |
| غياب أي import غير مصرح به لـ `legacy` في `pkg/spi/standards/` | اختبار النقاء أعلاه يفشل عند إضافة أي استيراد محظور | **محقق** |
| Contract Parity يثبت تكافؤ السلوك | نفس اختبار التكافؤ يشغّل دورة `ValidateDependencies` + `Initialize` + `Close` على المسارين ويقارن القدرات المتاحة | **محقق** |

---

## 3. توافق المسارين (Migration Compatibility)

| القدرة | مسار `WithStoragePorts` | مسار `WithCoreStorage` |
| --- | --- | --- |
| `Entries()` | `Ports.Entries` | محول فوق `CoreLedgerStore.GetEntry` |
| `Balance()` | `Ports.Balance` | `nil` (غير موجود في العقد القديم) |
| `MasterData()` | `Ports.MasterData` | `MasterDataStore()` إن وُجد، وإلا محول قراءة محدود |
| `Application()` | خدمة التطبيق العامة | خدمة التطبيق العامة |

لا تتغير بصمة واجهات `pkg/api/v1` ولا مخطط قاعدة البيانات في هذه المهمة. تغيّرت
توقيعات `pkg/spi/standards` و`pkg/sdk/templates` عمداً، ولذلك أُعيد توليد خط
الأساس العام عبر `./scripts/check_api_compat.sh --update` وأصبح في
`docs/api/v1.snapshot`.

---

## 4. المخاطر والمتابعة

- **الوحدات التي تعتمد على `Balance`:** على المسار القديم ستكون `nil`؛ يجب أن تعلن الوحدة ذلك عبر `ValidateDependencies` وتفشل برسالة واضحة، لا أن تفترض رصيداً صفرياً.
- **إزالة المسار القديم:** تبقى خطة إزالة `WithCoreStorage` مرتبطة بسياسة التوافق وإصدار MAJOR لاحق.
- **التوثيق:** تم تحديث [docs/compatibility_policy.md](../compatibility_policy.md) بقسم هجرة `StandardModule`.
