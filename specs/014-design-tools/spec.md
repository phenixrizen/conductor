# Native design artifacts and checks

**Implemented:** pinned native Spec Kit 1.0.6 and ADRKit CLI 0.13.0 commands run in
Conductor's isolated execution image. Real upstream-command and Docker acceptance
verify generated patches and independently executed native checks. These tools do
not establish architecture acceptance or production outcomes.

`conductor-design-tools` provides a bounded adapter for core project scaffolding,
Spec Kit spec/plan/tasks/checklist templates and artifact prerequisites, ADR corpus
lint, decision-to-path lookup, decision graphs and new Proposed ADRs. Native tool
commands operate on a temporary snapshot of the selected artifacts, with fixed
runtime paths and a scrubbed environment. Native helper code comes from the pinned
image, never a script selected from the managed repository.

Creation uses exclusive new files. Existing constitutions, templates, plans and
historical ADRs remain intact. Initializing an existing Spec Kit project with any
conflicting generated path is blocked. New ADRs always use native
`adr new --status proposed`; there is no accepted-status option. A created core
template remains an unfinished document requiring engineering work.

Read checks stage exact bounded source and never modify the repository's
`.specify/feature.json`. A maximum of 256 files, 64 KiB per file, 4 MiB combined
text and 1,024 traversed entries prevents unbounded discovery. Symlinks, special
files, binary input and exceeded bounds produce unavailable evidence. Native
subprocesses receive at most 30 seconds and retain at most 1 MiB of output;
truncation cannot yield a passing check.

Reports identify the native tool/version/commit, exact command, selected artifact
paths/digests/modes, input digest, output digest, created files and outcome. The
existing execution result additionally binds the operator profile, immutable image,
package/graph/task input, patch and original/resulting trees. Temporal remains the
sole durable execution authority; the adapter creates no parallel run state.

Checks have deliberately narrow meanings. Spec Kit prerequisites verify that
required artifacts exist; they do not semantically approve requirements or prove
implementation. ADRKit lint validates its schema, and check/explain resolves
applicable decisions. Source-recorded ADR status is context, not a Conductor
approval. Native failing checks remain failed. Authenticated artifact/graph reads
provide resulting text to other developers and agents under current source grants.

Spec Kit agent slash commands are prompt workflows, not standalone model-free CLI
commands. The core generated prompt artifacts can guide approved coding agents;
this adapter does not pretend to execute `/speckit.analyze` as a deterministic
architecture proof. ADRKit's separate Spec Kit extension is not installed: its
published 0.1.3 compatibility range excludes the selected Spec Kit 1.0.6 release.
