# Native topology review v0.1

2026-10-08 · #21 / #42. [Implementation and completion map](../implementation/NATIVE_TOPOLOGY_v0.1.md).

| Requirement | Retained verification |
| --- | --- |
| Semantic lifecycle and immutable identities | `TestTopologyLifecycleImmutableIdentitiesAndPrivateImages`, native create/delete/recreate/Bridge tree/move/Bond/paired Patch/allocation groups |
| Explicit membership and unknown preservation | `TestTopologyRejectsImplicitStealingAndStaleOriginals`, `TestTopologySplitClearsBondOnlySettingsAndPreservesUnknownMap`, actual system device merge/split and unknown-map move/rollback |
| Atomic native guards and foreign references | `TestTopologyNativePlansPreserveUnknownConfigurationAndGuardForeignReferences`, `TestNativeTopologySafety` late field/membership/foreign weak reference rejection and compensation conflict |
| Public privacy and typed contracts | `TestTopologyPublicViewsNeverExposePrivateConfiguration`, closed topology command tests; unknown images remain manager-private |
| Strict IPC transport | `TestStrictTopologyOriginalsRoundTripNativeSummariesAndSeal`: nonempty signed summaries survive read/validate/prepare, preserving empty sets and zero; modified, duplicate, unknown or case-ambiguous summary fields cannot bypass validation |
| Current capability, ceiling and root admission | `TestTopologyValidationRequiresIndependentCapabilityAndCurrentCeiling`, root object/name and stale dependency tests |
| Frontend execution gate | Shared topology Safe Apply permission regression plus the formal normal flow; independent topology authority, server availability, current usable execution validation and desktop responsibility remain required |
| Recovery and actual forwarding | Kernel system devices retained after Bond rollback, deleted rows GC and fresh identities, ofport runtime proof, Patch type recovery, lost reply without replay; real management-path Port move loses reachability and guarded rollback restores forwarding |
| System device identity | Same-name Linux device replacement blocks unsent creation, Applied proof and compensation; private original ifindex bindings are never adopted from a replacement |
| Formal browser normal flow | `native topology stages…`: actual Go/OVS Candidate, readable Diff, native apply, rollback/GC, retired Interface Audit and keyboard navigation |
| Browser exceptions | `topology drift…`, `topology reader…`: external map preserved, restage required, Reader/withheld configuration, provider stop/recovery, no unauthorized mutations |
| Depth and responsive responsibilities | Standard/Expert editor and Diff screenshots; tablet/mobile review with validation disabled and direct editor staging disabled, no page overflow |

Native CI has independent amd64/arm64 jobs. Each runs Core, Fields and Safety groups for schemas 3.3.9, 3.7.1 and 4.0.0 with no missing/skipped subcases accepted. These jobs join the existing complete runtime/race/systemd/authentication/TLS/storage/management recovery, formal browser, shared unit/integration/prototype browser and build checks. CI Gate requires all eight jobs to succeed.

Final acceptance requires the exact tested PR head, an identical merged main tree, independent complete main CI, downloaded report/artifact digests and visual review. Local Windows checks and Linux cross compilation alone are insufficient. Acceptance receipts and immutable tag metadata record run IDs, verified counts and screenshots after those gates pass. #71 remains an independent open diagnostic issue.
