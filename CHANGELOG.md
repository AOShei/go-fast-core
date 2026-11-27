# Changelog

The module version is the Go module's tag. The schema version is the `schema_version` every document carries; it changes only when a field is removed or its meaning changes (README, "Schema evolution").

## v0.1.15 (schema 0.2)

- **`vba.ReadBytes` reports a damaged file even when the project cannot
  be read.** When the compound file opens but `Read` fails (no project,
  or its `dir` stream cannot be read), the error now comes with a
  `Project` that holds only `FileDamaged`; it was nil. When the file does
  not open, the `Project` is still nil. go-fast-ooxml asked for it, so
  that `ReadBytes` can replace its `cfb.Open` and `vba.Read`.

## v0.1.14 (schema 0.2)

Additions to the shared readers that their users asked for; nothing
they did before changes.

- **`xmlscan`:** `Stats.CharRefsReplaced` counts character references to
  no character XML allows (`&#0;`, a surrogate, a number past U+10FFFF),
  each read as U+FFFD, so that a library can report the replacement
  character as such (`text.unmapped_glyphs`). They are still counted in
  `Repairs` too.
- **`opc`:** `Written` gives the name of a part made by `FromParts` as
  `Part.Name` gives it (forward slashes, no leading slash), with its own
  capitals, as it does for a zip member.
- **`cfb` and `vba`:** the limits are exported, `cfb.MaxEntries`
  (262,144 directory entries) and `vba.MaxStream` (256 MiB), so that a
  message or document that states them follows them.
- **`vba`:** `Project.FileDamaged` is the compound file's `Damaged`, so
  that a caller of `ReadBytes` learns that the file was read past damage.
- **`vba`: a project that names UTF-16 (code page 1200 or 1201) is not
  read in pairs.** Its source is bytes in a code page; it is read as
  both copies before v0.1.12 read it, the ASCII kept and the rest U+FFFD
  and counted in `Undecoded`. Since v0.1.12 `charset` decoded it as
  UTF-16. go-fast-msbin found it.
- **`xmlscan` is faster in its default reading** (no spans, no entity
  resolver), for go-fast-ooxml above all: a start tag is tested for
  first and its span is set by the caller only when spans are on, and
  text of no more than one piece takes a short path that leaves out the
  checks that do nothing there. go-fast-ooxml v0.7.0 on this core runs
  0.7 % fewer instructions over its 788 corpus files and 0.6 % fewer on
  its three large files, a quarter and a third of what it gained over
  v0.6.0's own scanner (+2.80 % and +1.90 %, now +2.10 % and +1.27 %).
  Nothing read changes.

## v0.1.13 (schema 0.2)

- **Two more shared readers** (README, "Shared readers"): `rfc822`, the
  reading of Internet messages and MIME that go-fast-email had and
  go-fast-html had a copy of for MHTML, with email's reading of character
  sets; and `entity`, the HTML character references go-fast-html and
  go-fast-xml each had.

## v0.1.12 (schema 0.2)

- **Shared readers.** Nine packages that libraries each kept a copy of
  are here once (README, "Shared readers"): `charset`, `xmlscan`, `opc`,
  `cfb` (with `cfbtest`), `clean`, `symbol`, `label`, `hidden` and `vba`
  (with `vbatest`). Each keeps every fix its copies had. The libraries
  move to them in their own releases; nothing in the root package
  changes.
- **New in them:** `charset` decodes ISO-2022-JP and gives GBK byte 0x80
  as the euro sign; `xmlscan` has go-fast-xml's features as options
  (`Recover`, `Markup`, `Spans`, entity expansion, attributes by URI)
  beside go-fast-ooxml's interface; `opc` reads interleaved parts, which
  XPS uses, and opens a zip without a content-types table for the caller
  to judge (`Typed`); `label` has `Alpha`.
- **CI:** `xmlscan`'s worst-case timing test is skipped under the race
  detector, where it took the macOS job past its ten minutes.

## v0.1.11 (schema 0.2)

- **A continued block is joined past footnotes.** `Block.Continued`
  says that a block continues the nearest block of its kind before it,
  and footnotes may stand between the two: the notes at the foot of a
  page where a paragraph is cut, as go-fast-pdf gives them. The
  renderers joined a continued block only to the block written just
  before it, so such a paragraph stayed in two pieces with the notes
  between. Now, when only footnotes stand between them, `Text`,
  `Markdown`, `WriteText` and `WriteMarkdown` write the continued block
  first, joined, and the footnotes after it. Only footnotes are passed:
  any other block written between, such as a note, a heading, a footer
  included by `Furniture` or a part marker, keeps the pieces apart, as
  before. A part rendered by itself is unchanged, and so is the JSON.
- **README: go-fast-xml's row** names its 26 built-in profiles (was 13)
  and the children its profiles hand over.

## v0.1.10 (schema 0.2)

- **Two stored kinds, for Visio drawings in go-fast-ooxml:** `shape_data`
  (`StoredShapeData`), the data a shape of a drawing carries and does not
  draw, as a table after the shape's own text in its page; and
  `linked_data` (`StoredLinkedData`), a copy the file keeps of data from
  an outside source it links to, as a table in the stored part.
  `StoredKindNames` lists them, `Validate` accepts them, and the
  renderers leave them out unless `RenderOptions.Stored` or
  `StoredKinds` asks for them, like every stored kind. An addition: the
  schema is unchanged.

## v0.1.9 (schema 0.2)

- **`Format.Number` writes a whole number faster** through a plain format: text, digit placeholders with or without thousands grouping, perhaps a decimal point and decimals, text (`#,##0`, `0.00`, `"$"#,##0_)`, and so on), with no percent sign, scaling comma, exponent or fraction. A whole number under 10^15 is written straight from its digits; anything else takes the general steps as before. The output is the same: `TestWholeIsNumber` and the fuzz target `FuzzWhole` (in CI) hold it to the general steps over the LibreOffice table's codes, the built-in formats and codes at the edges, and an independent review compared 21.6 million calls more. On go-fast-msbin's numbers that miss its cache, a whole number takes 28 ns instead of 44. Measured in the libraries, with one worker: go-fast-msbin's 890 dev workbooks 1.45% faster (2.5% fewer instructions), go-fast-ooxml's Common Crawl batch about 1% faster and its corpus unchanged; the document JSON of all 3,248 workbooks is byte-identical.
- **A `Cache` not made by `NewCache` remembers nothing**, as the nil `*Cache` does. The zero value (`new(numfmt.Cache)`, or a `sync.Pool`'s `New` written so) panicked on its first `Number`. go-fast-msbin and go-fast-ooxml make theirs with `NewCache` and were not affected. Found by the review.

## v0.1.8 (schema 0.2)

- **`numfmt.Cache`: what `Format.Number` made of recent values, for a sheet's repeated values.** `NewCache(entries)`, `Cache.Number(f, dst, v, date1904)` and `Cache.Reset()`. A direct-mapped table keyed by the `*Format`, the value's bits and the date system, which starts at 64 entries and doubles, keeping its entries, up to the size given; `Reset` empties all of it and keeps its memory. `Cache.Number` gives `Format.Number`'s bytes and `Result` always: a test over the LibreOffice table at sizes from 1 to 65,536, and the fuzz target `FuzzCache`, now in CI, hold it. The nil `*Cache` is `Format.Number`. Measured in the libraries before release, with their output unchanged: go-fast-msbin's batch of 1,111 workbooks 7.9% faster at 4,096 entries with all workers, go-fast-ooxml's Common Crawl batch 1.6% faster at 256 with one worker. Each library sets its own size. An addition, using the standard library only.
- **CI fuzzes every target**, 20 s each, in a job of its own, and fails if a `Fuzz` function is not in its list.

## v0.1.7 (schema 0.2)

- **Package `numfmt`: a cell's value through its number format**, the engine go-fast-ooxml wrote and go-fast-msbin copied, moved here unchanged so that both use one: `Parse`, `General`, `Builtin`, `AppendGeneral`, `Format.Number`, `Format.Text`, `Format.PlainText`, `Format.IsDate`, `Result`. Its tests come with it, with the table of 7,392 formats and values that LibreOffice showed. The code is go-fast-ooxml v0.5.0's byte for byte, but for one comment naming the table's new place. An addition, using the standard library only.

## v0.1.6 (schema 0.2)

- **Package `ziprecover`: one way to open a zip archive, also one cut short**, for every library: `Open` gives a `*zip.Reader` with `inflate.Decompressor` registered. For an archive whose directory is lost, it finds the members by their own headers, makes a directory for them and returns a `Recovery`; `Recovery.Message` words the `input.repaired` warning. It is go-fast-ooxml's recovery, which go-fast-odf had copied, with two changes. A file cut short just after a zip stored in it was read as that stored zip, whose directory archive/zip finds at the file's end; it is now recovered. A stored member whose sizes follow its data (an EPUB's `mimetype`, as Go's zip writer makes it) ended the recovery; it now ends at its descriptor, found by its sizes and CRC-32. On 12,103 real files cut at 13 places each, it gives what that code gave in every other case, and opening them takes 31% less CPU. An addition, using the standard library and `inflate` only.
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
