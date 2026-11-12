# فهرس قرارات المعمارية (ADRs)

هذه القرارات **ملزمة** لكل عمل في المراحل 1–5. أي تغيير في القرار يتطلب ADR جديداً يحل محل القديم (Supersedes)، لا تعديلاً صامتاً.

| ADR | الموضوع | الحالة | تاريخ الاعتماد | الفجوات | المهام |
| --- | --- | --- | --- | --- | --- |
| [ADR-0001](./ADR-0001-period-state-machine.md) | Period State Machine | معتمد | 2026-11-12 | `GAP-GOV-01`, `GAP-GOV-02` | `P1-T1`, `P2-T4` |
| [ADR-0002](./ADR-0002-fail-closed-governance.md) | Fail-Closed Governance | معتمد | 2026-11-12 | `GAP-GOV-03`, `GAP-GOV-04` | `P1-T7` |
| [ADR-0003](./ADR-0003-atomic-posting-boundary.md) | Atomic Posting Boundary | معتمد | 2026-11-12 | `GAP-PST-01/04/05/06/07/09` | `P1-T2`, `P1-T5`, `P1-T6` |
| [ADR-0004](./ADR-0004-approval-fingerprint.md) | Approval Fingerprint | معتمد | 2026-11-12 | `GAP-PST-02`, `GAP-PST-03` | `P1-T3`, `P1-T4` |
| [ADR-0005](./ADR-0005-storage-parity-and-migrations.md) | Storage Parity & Migrations | معتمد | 2026-11-12 | `GAP-PST-06`, `GAP-AST-02`, `GAP-OPS-07` | `P0-T8`, `P1-T6`, `P1-T9`, `P4-T6` |
| [ADR-0006](./ADR-0006-module-boundaries.md) | Module Boundaries for ERP | معتمد | 2026-11-12 | `GAP-ERP-01` … `GAP-ERP-10` | `P5-T1` … `P5-T12` |

## قاعدة الاعتماد

1. كل ADR يذكر السياق، القرار، البدائل المرفوضة، النتائج، المخاطر، وتاريخ المراجعة.
2. كل ADR مرتبط بفجوة `GAP-*` ومهمة `P*-T*`.
3. الاعتماد يتطلب `ARCH` + الدور المختص (`SME` للقرارات المحاسبية، `DBA` لقرارات التخزين، `SRE` للتشغيل).
4. محضر المراجعة محفوظ في [`review-minutes-2026-11-12.md`](./review-minutes-2026-11-12.md).
