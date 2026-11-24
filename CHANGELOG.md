# Changelog

The module version is the Go module's tag. The schema version is the `schema_version` every document carries; it changes only when a field is removed or its meaning changes (README, "Schema evolution").

## Unreleased (schema 0.2)

- **Package `ziprecover`: one way to open a zip archive, also one cut short**, for every library: `Open` gives a `*zip.Reader` with `inflate.Decompressor` registered. For an archive whose directory is lost, it finds the members by their own headers, makes a directory for them and returns a `Recovery`; `Recovery.Message` words the `input.repaired` warning. It is go-fast-ooxml's recovery, which go-fast-odf had copied, with one fix: a file cut short just after a zip stored in it was read as that stored zip, whose directory archive/zip finds at the file's end. On 12,103 real files cut at 13 places each, it gives what that code gave in every other case, and opening them takes 31% less CPU. An addition, using the standard library and `inflate` only.
- **`inflate.Reader.InputOffset`**: where the stream ended, once `Read` has returned `io.EOF`. Fuzzed against the bytes `compress/flate` reads.

## v0.1.5 (schema 0.2)

- **Package `inflate`: a shared decompressor**, about twice as fast as the standard library (3,770 MB/s against `compress/flate`'s 1,740 on the deflated members of 3,783 ODF files). go-fast-pdf's decompressor, moved here so that every library can use it: `Raw` for zip members, `Zlib` for PDF streams, `Gzip` for `.gz` files, each in one pass into a slice; `NewRawReader` and `NewGzipReader` for a member or file too large to hold whole (they hold 96 KiB); and `Decompressor`, to register on a `zip.Reader` for its deflated members. Each gives the standard library's bytes, also for a damaged stream, with errors that tell the kinds of damage apart, and bounds its output with a limit. An addition: nothing else in core changes, and it uses the standard library only.

## v0.1.4 (schema 0.2)

- **`Text` and `Markdown` allocate their result once.** They size it from the document's text before rendering, and write into it directly instead of through a 4 KiB `bufio.Writer` over a builder that doubled as it grew. `WriteText` and `WriteMarkdown` write a `strings.Builder` or `bytes.Buffer` directly too; other writers are still buffered. On a document of 800 paragraphs, `Text` allocates 90 KB in 2 allocations instead of 355 KB in 12, and takes 14 µs instead of 35 µs. The output is unchanged: a test and the fuzz target render every document through each kind of writer and compare.

## v0.1.3 (schema 0.2)

- **`Table.RowLocs`** (JSON `row_locs`): where each row of a table came from, for a source that can say it (a byte span, an anchor, a box). Empty, or one `Location` per row in the order of `Rows`; `Validate` checks the count and each location. An addition: tables without it are written as before, and the schema stays 0.2.

## v0.1.2 (schema 0.2)

- **Seven shared warning codes** for events that several libraries raised under codes of their own, or not at all. Each library now raises the shared code, at the severity given, and never one of its own for the same event:
  - `text.control_characters_removed` (info), replacing `ooxml.`, `odf.`, `rtf.` and `msbin.control_characters_removed`, and raised now by pdf, html and email, which removed them silently;
  - `text.encoding_detected` (warning), for input that declares no encoding and is read in one recognised from its bytes (go-fast-html's `html.charset_detected` before its release);
  - `input.unread_content` (warning), replacing the four `…unread_content`;
  - `input.stream_damaged` (warning), replacing `…part_damaged` and `…properties_damaged`: a stream of the file, not a `Part` of the document;
  - `input.decrypted` (info), replacing `ooxml.` and `msbin.decrypted`, and raised now by pdf and odf;
  - `table.region_split` (info) and `table.formula_no_value` (info), replacing the libraries' codes of those names.
  
  Additions only: nothing in core changes, and the schema stays 0.2. The README says the rule.

- **Documentation.** The "Bounded input" convention and `WarnLimitExceeded` now say what a fixed bound must report. It must report anything that would appear in the output, names and metadata included, and always under `limit.exceeded` at `error`. A bound on data used only inside a library needs no warning, and a bound that ordinary files reach is raised. No code changes.

## v0.1.1 (schema 0.2)

- **Warnings are bounded.** `Part.Warn` and `Document.Warn` keep at most `MaxWarnings` (1000) warnings on a part or a document. Past that, each further warning is counted in one last warning, `warnings.omitted` (`WarnWarningsOmitted`). Its severity is the highest of those it counts, and its message begins with their number. The count is read back from that warning, so it survives copies of a part and a round trip through JSON. It is added last; an extractor that appends to `Warnings` directly may put entries after it, and such appends are not bounded. The count stops at 999,999,999. Past the bound `Warn` adds no entry, so `Has` does not see a warning that was only counted. Before this, a file that raised a warning per code page or per charset label could give hundreds of thousands of them, and JSON many times its own size.
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
