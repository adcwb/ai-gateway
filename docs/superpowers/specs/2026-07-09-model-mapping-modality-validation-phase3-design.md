# Model mapping modality-consistency validation (phase 3 of the multimodal media adapters project) — design

## Context

Phase 1 (image/audio) and phase 2 (video) added `AIModelItem.ModelType` (`llm`/`image`/`tts`/`asr`/`video`) and made every media endpoint's model resolution (`resolveMediaModel`/`mediaCandidates`) filter candidates by modality at request time — a mapping or fallback-chain entry pointing at a model of the wrong modality is silently treated as "not a qualifying candidate" and skipped. That runtime enforcement already makes cross-modality routing safe; nothing was ever actually broken.

What's left is a narrower, real gap: the admin API (`internal/biz/model_mapping_admin.go`) and console (`ModelMappings.tsx`) have no concept of modality at all. An operator can create an image-generation mapping whose fallback chain accidentally names a TTS model, save it successfully, and only discover the mistake when a real request silently falls through every fallback candidate and fails — the config error surfaces at request time, not at save time.

**Scope correction made during brainstorming**: `AIModelItem` cataloging was never mandatory for chat/LLM routing — `mappingFallbackCandidates` and the router's provider-pool fallback both let a chat fallback chain name a model that has no catalog row at all (chat routing reads `AIProvider.Models`, not `AIModelItem`). Validating fallback-chain modality consistency for `llm` mappings would be a new, backward-incompatible restriction on existing, working chat-routing configs. So this phase's validation applies **only** to non-`llm` mappings (image/tts/asr/video) — chat mapping behavior is untouched.

Two other candidate gaps were considered and explicitly dropped after discussion:
- **Cross-modality virtual-model-name reuse** (one key using the same alias, e.g. `"premium"`, for both a chat model and an image model) — not a real need; different endpoints are already different namespaces in practice, no schema change.
- **A modality-filtered dropdown for fallback-chain entries** — the fallback chain's model field is free text, not a dropdown, so there's nothing to filter there; backend validation at save time is the correct safety net instead.

## Goal

1. **Backend**: `CreateModelMapping`/`UpdateModelMapping` verify `RealModelID` resolves to an existing `AIModelItem`; if that model's `ModelType != "llm"`, every `fallback_chain` entry must resolve to a cataloged, enabled `AIModelItem` with the same `ModelType`, or the request is rejected.
2. **Frontend**: the Model Mappings create/edit form gains a client-side-only "modality" selector that filters the "真实模型" dropdown's options to the selected modality.

## Non-goals

- No change to `llm`-modality mapping validation — chat routing keeps its existing "fallback chain names are free-form, no catalog requirement" behavior exactly as-is.
- No schema change to `AIModelMapping` (no modality column, no uniqueness change) — modality is entirely derived from `RealModel.ModelType`, never stored on the mapping itself.
- No change to the fallback-chain row's free-text model input — no dropdown/autocomplete added there.
- No change to runtime resolution (`resolveMediaModel`/`mediaCandidates`) — this phase is purely "catch the mistake earlier, at save time," not a new enforcement mechanism (the request-time enforcement already exists and is unchanged).

## Backend design

### New sentinel (`biz/errors.go`)

```go
ErrModelMappingModalityMismatch = kerrors.BadRequest("MODEL_MAPPING_MODALITY_MISMATCH", "fallback chain entry does not match the mapping's model type")
```

### Validation helper (`biz/model_mapping_admin.go`)

```go
// validateMappingModality loads realModelID's catalog row (rejecting an
// unresolvable id — previously unchecked) and, only when that model's
// ModelType isn't the "llm" default, verifies every fallback-chain entry
// resolves to a cataloged, enabled AIModelItem of the identical ModelType.
// llm mappings are exempt: AIModelItem cataloging was never mandatory for
// chat routing (mappingFallbackCandidates/the router's provider pool read
// AIProvider.Models, not AIModelItem), and this phase must not retroactively
// restrict that existing, working behavior.
func (uc *GatewayUseCase) validateMappingModality(ctx context.Context, realModelID uint, fallbackChain datatypes.JSON) error
```

Called from both `CreateModelMapping` and `UpdateModelMapping` whenever `RealModelID` and/or `FallbackChain` are part of the request (an update that touches neither is skipped, matching the existing partial-update convention). Reuses the exact `{"providerId":N,"model":"x"}` chain-entry shape `mappingFallbackCandidates` already parses.

## Frontend design

`ModelMappings.tsx`'s create/edit form gains one new piece of local state:

```ts
const [modalityFilter, setModalityFilter] = useState<string>("llm");
```

- A `<select>` next to "真实模型" listing `llm`/`image`/`tts`/`asr`/`video`. `ModelsPricing.tsx` already defines an identical `modelTypeOptions`/`modelTypeLabelKey` pair; this phase duplicates that same 5-entry static array locally in `ModelMappings.tsx` rather than exporting/importing it across pages — it's a constant, not logic, and a cross-page import for five string literals isn't worth the coupling.
- The "真实模型" `<select>`'s options become `models.filter(m => (m.modelType || "llm") === modalityFilter)` instead of the full unfiltered `models` list.
- `startEdit(m)`: when editing an existing mapping, initialize `modalityFilter` from the mapping's current real model's `modelType` (looked up in the already-fetched `models` list by `m.realModelId`), falling back to `"llm"` if not found.
- Changing the modality selector: if the currently selected `realModelId` no longer matches the new filter, reset it to `0` (the form's existing "unselected" sentinel, already used for the select's placeholder `<option value={0}>—</option>`) rather than silently keeping a now-hidden selection.
- Save-time validation errors (`MODEL_MAPPING_MODALITY_MISMATCH`) surface through the existing `actionError`/`ErrorBanner` catch — no new UI needed for the error path itself.

## Testing

`biz/model_mapping_admin_test.go` (new), table-driven:
- `llm` mapping, fallback chain naming an uncataloged model name → still succeeds (behavior-preservation regression test — this is the case that must NOT start failing).
- `image` mapping, fallback chain entry pointing at a `tts` model → rejected with `ErrModelMappingModalityMismatch`.
- `image` mapping, fallback chain entry pointing at another `image` model → succeeds.
- `RealModelID` pointing at a nonexistent `AIModelItem` → rejected.
- Update path: changing an `image` mapping's fallback chain to introduce a mismatched entry → rejected; changing only the description (no `RealModelID`/`FallbackChain` in the request) → validation skipped, succeeds.

Frontend: no new test file — this is a small filter-state change on an existing form; manual verification (open the form, switch modality, confirm the dropdown narrows) is sufficient given the console has no unit-test harness for individual pages today.
