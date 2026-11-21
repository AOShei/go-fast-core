# Changelog

The module version is the Go module's tag. The schema version is the `schema_version` every document carries; it changes only when a field is removed or its meaning changes (README, "Schema evolution").

## v0.1.1 (schema 0.2)

- **Warnings are bounded.** `Part.Warn` and `Document.Warn` keep at most `MaxWarnings` (1000) warnings on a part or a document. Past that, each further warning is counted in one last warning, `warnings.omitted` (`WarnWarningsOmitted`). Its severity is the highest of those it counts, and its message begins with their number. The count is read back from that warning, so it survives copies of a part and a round trip through JSON. If an extractor appends after it, the next `Warn` moves it back to the end. Past the bound `Warn` adds no entry, so `Has` does not see a warning that was only counted. Before this, a file that raised a warning per code page or per charset label could give hundreds of thousands of them, and JSON many times its own size.
- **`limit.exceeded` covers a library's fixed bounds.** The code was defined for `Options.Limits`; it now also reports what a library's own fixed bounds cut or leave out, with the message saying which bound. The README's "Bounded input" convention says so.
- The schema stays 0.2. Parts and documents with fewer than 1000 warnings are as before.

## v0.1.0 (schema 0.2)

The first release: the shared document model and extractor interface used by go-fast-pdf, go-fast-ooxml, go-fast-odf, go-fast-html, go-fast-rtf and go-fast-email.

- **Model.** `Document`, `Part`, `Block`, `Table`, `Image`, `Warning`, with JSON that has `[]` and never `null` for the lists a consumer iterates. `Validate` checks a document against the extractor contract and the text conventions.
- **Extractor interface.** `Extractor`, `Options` (workers, images, spans, password and keys, cells, part range, limits, child dispatcher) and the shared errors. `Options.ExtractChild` always returns a document for an embedded file, with a `child.*` warning when it was not extracted.
- **Display.** What a file declares about showing its content: `hidden` and `no_ink`, on blocks, spans, table regions and parts. Nothing else about visibility is judged.
- **Stored data.** The stored part and `Attrs["stored"]`, with thirteen kinds; `StoredKindNames()` lists them.
- **Closed sets.** `Attrs["stored"]`, `Attrs["type"]` on a note and `Attrs["role"]` take only the package's `Stored*`, `Note*` and `Role*` values, and `Validate` rejects any other.
- **Rendering.** `Markdown()`, `Text()`, `WriteMarkdown` and `WriteText`, with `RenderOptions` for furniture, stored data, part markers, display exclusion and children. `AddRenderFlags` gives every command the same five flags.
- **Budgets.** `TextBudget` (`Limits.MaxTextBytes`, with `BlockCost` and `CellCost`) and `DecodeBudget` (`Limits.MaxDecodedBytes`), shared with embedded documents through the context. `TextBudget.Exhaust` and `DecodeBudget.Settle`.
- **Ligatures.** `Ligature`, `ExpandLigatures` and `AppendLigatures` for the seven presentation-form ligatures.

### Schema history

- **0.2 (2026-11-20).** `Hidden` became `Display`, with only the reasons a file declares (`hidden`, `no_ink`); `hidden_checked` and the `text.hidden` warning were removed. Later in 0.2, two rules narrowed what an extractor may write without changing the shape: a thematic break is always `native`, and the values of `stored`, a note's `type` and `role` are closed sets.
- **0.1 (2026-11-18).** Agreed with go-fast-pdf over two review rounds.
